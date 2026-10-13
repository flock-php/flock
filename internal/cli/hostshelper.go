package cli

import (
	"github.com/spf13/cobra"

	"flock/internal/hosts"
)

// newHostsHelperCommand is the entry point of the Windows hosts helper service.
func newHostsHelperCommand() *cobra.Command {
	var source, tld string
	cmd := &cobra.Command{
		Use:    "hosts-helper",
		Short:  "Copy the site domains into the hosts file (the Windows service installed by `flock install`)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return hosts.RunHelper(source, tld)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "file the daemon lists its domains in")
	cmd.Flags().StringVar(&tld, "tld", "", "only names under this TLD are accepted")
	return cmd
}
