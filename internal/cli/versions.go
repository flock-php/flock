package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"

	"github.com/spf13/cobra"

	"flock/internal/node"
	"flock/internal/php"
)

func newPHPCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "php", Short: "Manage PHP versions"}

	var from string
	install := &cobra.Command{
		Use:   "install <version> | --from <dir|tar.gz>",
		Short: "Install a PHP version (e.g. 8.4, its latest release), or a local build with --from",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			switch {
			case from != "" && len(args) > 0:
				return errors.New("give either a version or --from, not both")
			case from == "" && len(args) == 0:
				return errors.New("which version? e.g. `flock php install 8.4`")
			case from == "":
				return installPHPVersion(cmd, s, args[0])
			}

			minor, err := php.InstallFrom(s.root, from)
			if err != nil {
				return err
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("installed PHP %s\n", minor)
			return nil
		},
	}
	install.Flags().StringVar(&from, "from", "", "directory or archive containing the php and php-fpm binaries")

	list := &cobra.Command{
		Use:   "list",
		Short: "List installed PHP versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			versions, err := php.Installed(s.root)
			if err != nil {
				return err
			}
			for _, v := range versions {
				marker := " "
				if v == s.cfg.DefaultPHP {
					marker = "*"
				}
				cmd.Printf("%s %s\n", marker, v)
			}
			return nil
		},
	}

	cmd.AddCommand(install, list)
	return cmd
}

// installPHPVersion installs the newest release of minor through the daemon, which downloads it
// from the manifest, and waits for it. Ctrl+C cancels the download.
func installPHPVersion(cmd *cobra.Command, s *state, minor string) error {
	if php.MinorOf(minor) != minor {
		return fmt.Errorf("give a version like 8.4 (Flock installs its latest release), not %q", minor)
	}
	if installed, _ := php.Installed(s.root); slices.Contains(installed, minor) {
		cmd.Printf("PHP %s is already installed\n", minor)
		return nil
	}

	v, err := phpRelease(cmd.Context(), s.client(), minor)
	if err != nil {
		return err
	}
	if v.Latest == "" {
		return fmt.Errorf("no PHP %s build is published for this platform", minor)
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	return installPHP(ctx, s, minor, cmd.OutOrStdout())
}

func newUseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "use <php-version>",
		Short: "Set the default PHP version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			installed, _ := php.Installed(s.root)
			if !slices.Contains(installed, args[0]) {
				return &phpMissingError{minor: args[0]}
			}
			s.cfg.DefaultPHP = args[0]
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("default PHP is now %s\n", args[0])
			return nil
		},
	}
}

func newNodeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "node", Short: "Manage Node.js versions"}

	install := &cobra.Command{
		Use:   "install <version>",
		Short: "Install a Node.js version (major like 22, or exact like 22.11.0)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return installNode(ctx, s, args[0], cmd.OutOrStdout())
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List installed Node.js versions",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			versions, err := node.Installed(s.root)
			if err != nil {
				return err
			}
			for _, v := range versions {
				cmd.Println(v)
			}
			cmd.Printf("default: %s (projects can override with .nvmrc / .node-version)\n", s.cfg.DefaultNode)
			return nil
		},
	}

	def := &cobra.Command{
		Use:   "default <major>",
		Short: "Set the default Node.js version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			s.cfg.DefaultNode = args[0]
			return s.cfg.Save(s.root)
		},
	}

	cmd.AddCommand(install, list, def)
	return cmd
}
