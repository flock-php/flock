package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"flock/internal/daemon"
	"flock/internal/paths"
)

func newDaemonCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run the Flock daemon in the foreground (normally started by `flock install`)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := paths.Root()
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cmd.Printf("flock daemon %s — API on %s\n", Version, paths.Socket(root))

			return daemon.Run(ctx, daemon.Config{Root: root, Version: Version})
		},
	}
}
