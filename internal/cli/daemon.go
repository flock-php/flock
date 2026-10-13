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
	var tld, dnsAddr string

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the Flock daemon in the foreground",
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := paths.Root()
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cmd.Printf("flock daemon %s — DNS *.%s on %s, API on %s\n", Version, tld, dnsAddr, paths.Socket(root))

			return daemon.Run(ctx, daemon.Config{Root: root, TLD: tld, DNSAddr: dnsAddr, Version: Version})
		},
	}

	cmd.Flags().StringVar(&tld, "tld", "test", "top-level domain served by Flock")
	cmd.Flags().StringVar(&dnsAddr, "dns-addr", "127.0.0.1:53535", "address of the embedded DNS server")

	return cmd
}
