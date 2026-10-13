package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"flock/internal/daemon"
	"flock/internal/paths"
)

// exitAlreadyRunning tells the watchdog not to restart a daemon that found another one running.
const exitAlreadyRunning = 3

func newDaemonCommand() *cobra.Command {
	var logFile string
	var watchdog bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the Flock daemon in the foreground (normally started by `flock install`)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := paths.Root()
			if err != nil {
				return err
			}

			if logFile != "" {
				f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					return err
				}
				redirectOutput(f)
				cmd.SetOut(f)
				cmd.SetErr(f)
			}
			if watchdog {
				return runWatchdog(cmd, logFile)
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cmd.Printf("flock daemon %s — API on %s\n", Version, paths.Socket(root))

			err = daemon.Run(ctx, daemon.Config{Root: root, Version: Version})
			if errors.Is(err, daemon.ErrAlreadyRunning) {
				cmd.PrintErrln("Error:", err)
				os.Exit(exitAlreadyRunning)
			}
			return err
		},
	}

	cmd.Flags().StringVar(&logFile, "log-file", "", "append all output to this file (the background daemon has no console)")
	cmd.Flags().BoolVar(&watchdog, "watchdog", false, "run the daemon as a child process and restart it when it crashes (Windows)")
	_ = cmd.Flags().MarkHidden("watchdog")
	return cmd
}
