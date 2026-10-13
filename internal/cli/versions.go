package cli

import (
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"flock/internal/daemon"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/postgres"
)

func newPHPCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "php", Short: "Manage PHP versions"}

	var from string
	install := &cobra.Command{
		Use:   "install --from <dir|tar.gz>",
		Short: "Install a PHP build (a directory or .tar.gz with php and php-fpm)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" {
				return fmt.Errorf("--from is required until Flock publishes PHP builds (build one with build/php/build.sh)")
			}
			s, err := load()
			if err != nil {
				return err
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
				return fmt.Errorf("PHP %s is not installed (installed: %v)", args[0], installed)
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
			v, err := (&node.Installer{Root: s.root}).Install(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			cmd.Printf("installed Node %s\n", v)
			return nil
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

func newPostgresCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "postgres", Short: "Manage the PostgreSQL service"}

	install := &cobra.Command{
		Use:   "install [version]",
		Short: "Install and start PostgreSQL (user postgres, no password, port 5432)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version := postgres.DefaultVersion
			if len(args) > 0 {
				version = args[0]
			}
			s, err := load()
			if err != nil {
				return err
			}

			inst, err := postgres.NewInstaller(s.root)
			if err != nil {
				return err
			}
			cmd.Printf("installing PostgreSQL %s…\n", version)
			if err := inst.Install(cmd.Context(), version); err != nil {
				return err
			}
			if err := postgres.InitDB(inst.BinDir(version), daemon.PostgresDataDir(s.root)); err != nil {
				return err
			}

			s.cfg.Services.Postgres = version
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("PostgreSQL %s on 127.0.0.1:%d (user postgres, no password)\n", version, s.cfg.Ports.Postgres)
			return nil
		},
	}

	createDB := &cobra.Command{
		Use:   "create-db <name>...",
		Short: "Create databases (no-op when they exist)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			for _, name := range args {
				if err := postgres.CreateDatabase(cmd.Context(), s.cfg.Ports.Postgres, name); err != nil {
					return fmt.Errorf("create %s: %w", name, err)
				}
				cmd.Printf("database %s ready\n", name)
			}
			return nil
		},
	}

	cmd.AddCommand(install, createDB, newPostgresClientCommand())
	return cmd
}
