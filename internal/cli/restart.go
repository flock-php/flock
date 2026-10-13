package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
)

func newRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the background daemon and all services",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			before, _ := s.client().Status(cmd.Context())
			if err := restartService(); err != nil {
				return err
			}

			// Wait for a new daemon (different PID) to answer.
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				st, err := s.client().Status(cmd.Context())
				if err == nil && st.PID != before.PID {
					cmd.Printf("restarted (pid %d)\n", st.PID)
					for _, w := range st.Warnings {
						cmd.Println("warning: " + w)
					}
					return nil
				}
				if err != nil && !errors.Is(err, api.ErrDaemonNotRunning) {
					return err
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not come back within 15s — see `flock logs daemon`")
		},
	}
}
