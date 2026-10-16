package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"flock/internal/binaries"
)

// newChannelCommand switches the channel of prebuilt packages. Hidden: the apps offer beta as a
// switch; experimental (everything published, before promotion) is for testing new packages.
func newChannelCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "channel [stable|beta|experimental]",
		Short:  "Show or switch the channel Flock installs packages from",
		Hidden: true,
		Args:   cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			current, err := binaries.ParseChannel(s.cfg.Channel)
			if err != nil {
				current = binaries.Stable
			}
			if len(args) == 0 {
				cmd.Println(current)
				return nil
			}
			ch, err := binaries.ParseChannel(args[0])
			if err != nil {
				return err
			}
			if ch == current {
				cmd.Printf("already on the %s channel\n", ch)
				return nil
			}
			s.cfg.Channel = string(ch)
			if ch == binaries.Stable {
				s.cfg.Channel = ""
			}
			if err := s.commit(cmd.Context(), cmd.OutOrStdout()); err != nil {
				return err
			}
			cmd.Printf("switched to the %s channel\n", ch)
			if ch == binaries.Experimental {
				fmt.Fprintln(cmd.OutOrStdout(), "packages that aren't promoted yet are offered now; `flock channel stable` goes back")
			}
			return nil
		},
	}
}
