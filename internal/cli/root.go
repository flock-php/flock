// Package cli implements the `flock` command.
package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time: go build -ldflags "-X flock/internal/cli.Version=1.2.3".
var Version = "dev"

// NewRootCommand builds the `flock` command tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "flock",
		Short:         "Flock — a native local Laravel development environment",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(newDaemonCommand(), newStatusCommand(), newVersionCommand())

	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Flock version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println("flock " + Version)
		},
	}
}
