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

			before, err := s.client().Status(cmd.Context())
			if err == nil && cmd.Name() == "start" {
				cmd.Printf("already running (pid %d)\n", before.PID)
				return nil
			}
			if err := restartService(); err != nil {
				return err
			}

			// Wait for a new daemon (different PID) to answer.
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				st, err := s.client().Status(cmd.Context())
				if err == nil && st.PID != before.PID {
					cmd.Printf("%sed (pid %d)\n", cmd.Name(), st.PID) // restarted, started
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

// newStartCommand starts the daemon after `flock stop` (restartService also loads it again) and
// leaves a running one alone.
func newStartCommand() *cobra.Command {
	cmd := newRestartCommand()
	cmd.Use, cmd.Short = "start", "Start the background daemon and all services after `flock stop`"
	return cmd
}

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon and all services until `flock start`",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := stopDaemon(); err != nil {
				return err
			}
			cmd.Println("stopped")
			return nil
		},
	}
}
