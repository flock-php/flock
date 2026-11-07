package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
)

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the background daemon and all services until `flock start`",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			// Only to know the pid, as for `flock restart`: a daemon too busy to answer is stopped all the same.
			st, statusErr := s.client().Status(cmd.Context())
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			// Also when the daemon doesn't answer: the service may be about to start it again.
			if err := stopDaemon(cmd.Context(), s.client(), st.PID); err != nil {
				return err
			}
			// Stopping doesn't change the service: it still starts the daemon at login (Linux: with the computer).
			switch {
			case errors.Is(statusErr, api.ErrDaemonNotRunning):
				cmd.Println("not running")
			case st.PID == 0:
				cmd.Printf("stopped; it starts again with `flock start`, or %s\n", serviceStarts)
			default:
				cmd.Printf("stopped (pid %d); it starts again with `flock start`, or %s\n", st.PID, serviceStarts)
			}
			return nil
		},
	}
}

func newStartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the background daemon and all services after `flock stop`",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			if st, err := s.client().Status(cmd.Context()); err == nil {
				cmd.Printf("already running (pid %d)\n", st.PID)
				return nil
			}
			if err := startDaemon(); err != nil {
				return err
			}

			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				st, err := s.client().Status(cmd.Context())
				if err == nil {
					cmd.Printf("started (pid %d)\n", st.PID)
					for _, w := range st.Warnings {
						cmd.Println("warning: " + w)
					}
					return nil
				}
				if !errors.Is(err, api.ErrDaemonNotRunning) {
					return err
				}
				time.Sleep(200 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not start within 15s, see `flock logs daemon`")
		},
	}
}
