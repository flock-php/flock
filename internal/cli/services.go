package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"flock/internal/dumps"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/valkey"
)

func newXdebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "xdebug [on|off]",
		Short:     "Load Xdebug for all PHP versions (debug sessions start only with XDEBUG_TRIGGER)",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				state := "off"
				if s.cfg.Xdebug {
					state = "on"
				}
				cmd.Printf("xdebug is %s\n", state)
				return nil
			}
			if args[0] != "on" && args[0] != "off" {
				return fmt.Errorf("expected on or off, got %q", args[0])
			}

			s.cfg.Xdebug = args[0] == "on"
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}

			// The daemon writes the ini files; when it is not running, do it here so the CLI agrees.
			installed, _ := php.Installed(s.root)
			for _, v := range installed {
				if err := php.SetXdebug(s.root, v, s.cfg.Xdebug); err != nil {
					return err
				}
			}

			if s.cfg.Xdebug {
				cmd.Println("xdebug on — listen on port 9003 in your IDE, then trigger a session with the Xdebug browser extension,")
				cmd.Println("?XDEBUG_TRIGGER=1 in the URL, or `XDEBUG_TRIGGER=1 php artisan ...`")
			} else {
				cmd.Println("xdebug off")
			}
			return nil
		},
	}
	return cmd
}

func newDumpsCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "dumps [on|off|clear]",
		Short: "Send dump()/dd() output to Flock instead of the page or terminal, or list captured dumps",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			switch {
			case len(args) == 0:
				entries, err := dumps.List(s.root)
				if err != nil {
					return err
				}
				state := "off"
				if s.cfg.Dumps {
					state = "on"
				}
				cmd.Printf("dump capture is %s — %d dump(s)\n", state, len(entries))
				for i := len(entries) - 1; i >= 0; i-- {
					e := entries[i]
					where := ""
					if e.Source != nil {
						where = fmt.Sprintf("%s:%d", e.Source.File, e.Source.Line)
					}
					cmd.Printf("\n── %s %s\n%s\n", e.Time.Local().Format("15:04:05"), where, e.Text)
				}
				return nil
			case args[0] == "clear":
				return dumps.Clear(s.root)
			case args[0] == "on" || args[0] == "off":
				s.cfg.Dumps = args[0] == "on"
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				cmd.Printf("dump capture %s\n", args[0])
				return nil
			}
			return fmt.Errorf("expected on, off or clear, got %q", args[0])
		},
	}
}

func newRedisCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "redis", Short: "Manage the Redis service (Valkey)"}

	var from string
	install := &cobra.Command{
		Use:   "install --from <dir>",
		Short: "Install and start Valkey on 127.0.0.1:6379 (build one with build/valkey/build.sh)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" {
				return fmt.Errorf("--from is required until Flock publishes builds")
			}
			s, err := load()
			if err != nil {
				return err
			}

			version, err := valkey.InstallFrom(s.root, from)
			if err != nil {
				return err
			}
			s.cfg.Services.Redis = version
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("Valkey %s on 127.0.0.1:%d (REDIS_HOST=127.0.0.1, REDIS_PORT=%d, no password)\n", version, s.cfg.Ports.Redis, s.cfg.Ports.Redis)
			return nil
		},
	}
	install.Flags().StringVar(&from, "from", "", "directory containing valkey-server and valkey-cli")

	cmd.AddCommand(install)
	return cmd
}

func newPostgresClientCommand() *cobra.Command {
	var from string

	cmd := &cobra.Command{
		Use:   "install-client --from <dir>",
		Short: "Install psql, pg_dump, pg_restore, ... (build them with build/postgres-client/build.sh)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from == "" {
				return fmt.Errorf("--from is required until Flock publishes builds")
			}
			s, err := load()
			if err != nil {
				return err
			}

			version, err := postgres.InstallClientFrom(s.root, from)
			if err != nil {
				return err
			}
			s.cfg.Services.PostgresClient = version
			if err := s.cfg.Save(s.root); err != nil {
				return err
			}
			cmd.Printf("PostgreSQL %s client tools installed (connect to Flock's server by default)\n", version)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "directory with bin/ and lib/ from the client build")

	return cmd
}
