package cli

import (
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/paths"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the state of the Flock daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := paths.Root()
			if err != nil {
				return err
			}

			status, err := api.NewClient(paths.Socket(root)).Status(cmd.Context())
			if err != nil {
				return err
			}

			cmd.Printf("daemon  running (pid %d, %s, up %s)\n", status.PID, status.Version, time.Since(status.StartedAt).Round(time.Second))
			cmd.Printf("dns     *.%s → 127.0.0.1 on %s\n", status.DNS.TLD, status.DNS.Addr)

			return nil
		},
	}
}
