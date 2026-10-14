package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"

	"github.com/spf13/cobra"

	"flock/internal/components"
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

func newValkeyCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "valkey", Short: "Manage Valkey, the open-source Redis (macOS)"}

	var from string
	install := &cobra.Command{
		Use:   "install [--from <dir>]",
		Short: "Build the newest Valkey from source and start it on 127.0.0.1:6379",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return installValkey(ctx, s, from, cmd.OutOrStderr(), cmd.ErrOrStderr())
		},
	}
	install.Flags().StringVar(&from, "from", "", "directory containing valkey-server and valkey-cli (skips the build)")

	cmd.AddCommand(install)
	return cmd
}

// errNoValkey: Valkey has no Windows build; the catalog's Redis takes its place there.
var errNoValkey = errors.New("Valkey isn't available on Windows — install Redis instead: `flock services install redis`")

// installValkey installs Valkey from a build directory (from) or builds the newest from source,
// and starts it.
func installValkey(ctx context.Context, s *state, from string, out, errOut io.Writer) error {
	if runtime.GOOS == "windows" {
		return errNoValkey
	}
	var version string
	var err error
	if from != "" {
		version, err = valkey.InstallFrom(s.root, from)
	} else {
		version, err = buildComponent(ctx, errOut, "Valkey", components.DefaultSources().LatestValkey, components.BuildValkey,
			func(dir string) (string, error) { return valkey.InstallFrom(s.root, dir) })
	}
	if err != nil {
		return err
	}

	s.cfg.Services.Valkey = version
	if err := s.commit(ctx, errOut); err != nil {
		return err
	}
	fmt.Fprintf(out, "Valkey %s on 127.0.0.1:%d (REDIS_HOST=127.0.0.1, REDIS_PORT=%d, no password)\n", version, s.cfg.Ports.Valkey, s.cfg.Ports.Valkey)
	return nil
}

func newPostgresClientCommand() *cobra.Command {
	var from string

	cmd := &cobra.Command{
		Use:   "install-client [--from <dir>]",
		Short: "Install psql, pg_dump, pg_restore, ... (Windows: downloads them; macOS: builds them from source)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return installPostgresClient(ctx, s, from, cmd.OutOrStderr(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "directory with bin/ and lib/ from the client build (skips the build)")

	return cmd
}

// installPostgresClient installs the PostgreSQL client tools from a build directory (from),
// downloads the prebuilt ones where Flock has them (Windows) or builds them from source (macOS).
func installPostgresClient(ctx context.Context, s *state, from string, out, errOut io.Writer) error {
	var version string
	var err error
	if from != "" {
		version, err = postgres.InstallClientFrom(s.root, from)
	} else if b, ok := postgres.PrebuiltClient(runtime.GOOS, runtime.GOARCH); ok {
		p := newProgress(errOut)
		p.set(fmt.Sprintf("downloading PostgreSQL %s (%d MB; Flock keeps only the client tools)…", b.Version, b.Size>>20))
		version, err = postgres.DownloadClient(ctx, s.root, b, nil, downloadProgress(p, "PostgreSQL "+b.Version))
		p.clear()
	} else {
		src := components.DefaultSources()
		latest := func(ctx context.Context) (string, error) { return src.LatestPostgres(ctx, components.ClientMajor) }
		version, err = buildComponent(ctx, errOut, "the PostgreSQL client tools", latest, components.BuildPostgresClient,
			func(dir string) (string, error) { return postgres.InstallClientFrom(s.root, dir) })
	}
	if err != nil {
		return err
	}

	s.cfg.Services.PostgresClient = version
	if err := s.cfg.Save(s.root); err != nil {
		return err
	}
	fmt.Fprintf(out, "PostgreSQL %s client tools installed (connect to Flock's server by default)\n", version)
	return nil
}

// buildFunc builds a component from source (components.BuildValkey, components.BuildPostgresClient).
type buildFunc func(ctx context.Context, src components.Sources, workspace, version string, r *components.Reporter) (string, func(), error)

// buildComponent builds the newest version of a component from source, streaming the build's
// output to out, and installs the result with install.
func buildComponent(ctx context.Context, out io.Writer, what string, latest func(context.Context) (string, error), build buildFunc, install func(dir string) (string, error)) (string, error) {
	workspace, err := components.DefaultWorkspace()
	if err != nil {
		return "", err
	}
	version, err := latest(ctx)
	if err != nil {
		return "", fmt.Errorf("look up the newest version of %s: %w", what, err)
	}
	fmt.Fprintf(out, "building %s %s from source…\n", what, version)
	reporter := components.NewReporter(out, nil)
	dir, cleanup, err := build(ctx, components.DefaultSources(), workspace, version, reporter)
	if ctx.Err() != nil {
		return "", fmt.Errorf("canceled — %s %s was not installed", what, version)
	}
	if err != nil {
		return "", err
	}
	defer cleanup()
	return install(dir)
}

// downloadProgress shows a download's progress on p.
func downloadProgress(p *progress, what string) func(done, total int64) {
	return func(done, total int64) {
		if total > 0 {
			p.set(fmt.Sprintf("downloading %s (%d of %d MB)", what, done>>20, total>>20))
		} else {
			p.set(fmt.Sprintf("downloading %s (%d MB)", what, done>>20))
		}
	}
}
