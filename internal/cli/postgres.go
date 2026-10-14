package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/postgres"
)

func newPostgresCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "postgres", Short: "Manage the PostgreSQL service"}

	install := &cobra.Command{
		Use:   "install [major|version]",
		Short: "Install a PostgreSQL version (default 18) and start it when none runs yet (user postgres, no password, port 5432)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			major, version := postgres.DefaultMajor, ""
			if len(args) > 0 {
				major = args[0]
				if m := postgres.MajorOf(args[0]); m != "" {
					major, version = m, args[0]
				}
			}
			return postgresOperation(cmd, major, "installing", func(ctx context.Context, ops pgOps) error {
				return ops.Install(ctx, major, version)
			})
		},
	}

	use := &cobra.Command{
		Use:   "use <major>",
		Short: "Run another installed PostgreSQL version (each version keeps its own databases)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return postgresOperation(cmd, args[0], "switching to", func(ctx context.Context, ops pgOps) error {
				return ops.Activate(ctx, args[0])
			})
		},
	}

	update := &cobra.Command{
		Use:   "update [major]",
		Short: "Update a PostgreSQL version to its newest release, keeping its databases (default: the active version)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			major := ""
			if len(args) > 0 {
				major = args[0]
			} else {
				s, err := load()
				if err != nil {
					return err
				}
				if major = postgres.MajorOf(s.cfg.Services.Postgres); major == "" {
					return errors.New("which version? e.g. `flock postgres update 18`")
				}
			}
			return postgresOperation(cmd, major, "updating", func(ctx context.Context, ops pgOps) error {
				return ops.Update(ctx, major)
			})
		},
	}

	var deleteData bool
	uninstall := &cobra.Command{
		Use:   "uninstall <major> [--delete-data]",
		Short: "Remove a PostgreSQL version; its databases stay unless --delete-data is given",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return postgresOperation(cmd, args[0], "uninstalling", func(ctx context.Context, ops pgOps) error {
				return ops.Uninstall(ctx, args[0], deleteData)
			})
		},
	}
	uninstall.Flags().BoolVar(&deleteData, "delete-data", false, "also delete that version's databases")

	list := &cobra.Command{
		Use:   "list",
		Short: "List the PostgreSQL versions with what is installed, updates and data sizes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			ops, err := postgresOps(cmd.Context(), s)
			if err != nil {
				return err
			}
			v, err := ops.Versions(cmd.Context())
			if err != nil {
				return err
			}
			printPostgresVersions(cmd.OutOrStdout(), v)
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

	cmd.AddCommand(install, use, update, list, uninstall, createDB, newPostgresClientCommand())
	return cmd
}

// postgresOperation runs one change and prints the resulting state of major.
func postgresOperation(cmd *cobra.Command, major, what string, do func(ctx context.Context, ops pgOps) error) error {
	s, err := load()
	if err != nil {
		return err
	}
	// The daemon finishes an operation it started even when the CLI is interrupted.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	ops, err := postgresOps(ctx, s)
	if err != nil {
		return err
	}
	cmd.PrintErrf("%s PostgreSQL %s…\n", what, major)
	if err := do(ctx, ops); err != nil {
		return err
	}
	v, err := ops.Versions(ctx)
	if err != nil {
		return err
	}
	printPostgresVersions(cmd.OutOrStdout(), v)
	if m, ok := v.Find(major); ok && m.Active {
		state := "starts with the daemon"
		switch {
		case !v.Enabled:
			state = "switched off — start it in the Flock app"
		case v.Running:
			state = "running"
		}
		cmd.Printf("\nPostgreSQL %s on 127.0.0.1:%d (user postgres, no password): %s\n", m.Installed, v.Port, state)
	}
	return nil
}

func printPostgresVersions(out io.Writer, v postgres.Versions) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  VERSION\tINSTALLED\tLATEST\tDATA\tEND OF LIFE\t")
	for _, m := range v.Majors {
		marker := " "
		if m.Active {
			marker = "*"
		}
		installed, latest, data := dash(m.Installed), dash(m.Latest), "-"
		if m.Update {
			latest += " (update)"
		}
		if m.HasData {
			data = humanSize(m.DataSize)
		}
		if m.Busy != "" {
			installed += " (" + m.Busy + ")"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\t%s\t\n", marker, m.Major, installed, latest, data, dash(m.EOL))
	}
	_ = tw.Flush()
	if v.CheckError != "" {
		fmt.Fprintf(out, "could not look up the newest releases: %s\n", v.CheckError)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}

// pgOps changes PostgreSQL through the daemon, or directly when it doesn't run.
type pgOps interface {
	Versions(ctx context.Context) (postgres.Versions, error)
	Install(ctx context.Context, major, version string) error
	Update(ctx context.Context, major string) error
	Activate(ctx context.Context, major string) error
	Uninstall(ctx context.Context, major string, deleteData bool) error
}

func postgresOps(ctx context.Context, s *state) (pgOps, error) {
	c := s.client()
	_, err := c.PostgresVersions(ctx)
	if err == nil {
		return daemonPG{c}, nil
	}
	if !errors.Is(err, api.ErrDaemonNotRunning) {
		return nil, err
	}
	m, err := postgres.NewManager(s.root, &cliPGService{s: s})
	if err != nil {
		return nil, err
	}
	return directPG{m}, nil
}

type daemonPG struct{ c *api.Client }

func (d daemonPG) Versions(ctx context.Context) (postgres.Versions, error) {
	return d.c.PostgresVersions(ctx)
}
func (d daemonPG) Install(ctx context.Context, major, version string) error {
	_, err := d.c.InstallPostgres(ctx, major, version)
	return err
}
func (d daemonPG) Update(ctx context.Context, major string) error {
	_, err := d.c.UpdatePostgres(ctx, major)
	return err
}
func (d daemonPG) Activate(ctx context.Context, major string) error {
	_, err := d.c.ActivatePostgres(ctx, major)
	return err
}
func (d daemonPG) Uninstall(ctx context.Context, major string, deleteData bool) error {
	_, err := d.c.UninstallPostgres(ctx, major, deleteData)
	return err
}

type directPG struct{ m *postgres.Manager }

func (d directPG) Versions(ctx context.Context) (postgres.Versions, error) {
	if d.m.Catalog.Stale() {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		_ = d.m.Catalog.Check(ctx) // reported in check_error
	}
	return d.m.Versions(), nil
}
func (d directPG) Install(ctx context.Context, major, version string) error {
	return d.m.Install(ctx, major, version)
}
func (d directPG) Update(ctx context.Context, major string) error { return d.m.Update(ctx, major) }
func (d directPG) Activate(ctx context.Context, major string) error {
	return d.m.Activate(ctx, major)
}
func (d directPG) Uninstall(ctx context.Context, major string, deleteData bool) error {
	return d.m.Uninstall(ctx, major, deleteData)
}

// cliPGService edits config.json while the daemon doesn't run; it applies it when it starts.
type cliPGService struct{ s *state }

func (c *cliPGService) Active() (string, bool) {
	return c.s.cfg.Services.Postgres, c.s.cfg.Services.Running("postgres")
}

func (c *cliPGService) Apply(version string, enable bool) error {
	c.s.cfg.Services.Postgres = version
	if enable {
		c.s.cfg.Services.SetRunning("postgres", true)
	}
	return c.s.cfg.Save(c.s.root)
}

func (c *cliPGService) Running() bool { return false }
func (c *cliPGService) Port() int     { return c.s.cfg.Ports.Postgres }
func (c *cliPGService) Live() bool    { return false }
