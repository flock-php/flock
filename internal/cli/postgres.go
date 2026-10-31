package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/postgres"
)

// postgresOperation runs one change and prints the resulting state of major.
func postgresOperation(cmd *cobra.Command, major, what string, do func(ctx context.Context, ops pgOps) error) error {
	s, err := load()
	if err != nil {
		return err
	}
	// The daemon finishes an operation it started even when the CLI is interrupted.
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	return runPostgres(ctx, s, cmd.OutOrStdout(), cmd.ErrOrStderr(), major, what, do)
}

// isPostgres reports whether a service id names PostgreSQL, which `flock services` manages
// through its own manager (several majors side by side) instead of the catalog.
func isPostgres(id string) bool {
	switch strings.ToLower(id) {
	case "postgres", "postgresql", "pgsql":
		return true
	}
	return false
}

// installPostgres installs the newest build of a major (for `flock services install postgres`),
// with extensions. A version like "18.6" names its major; "" is the default one.
func installPostgres(ctx context.Context, s *state, version string, extensions []string, out, errOut io.Writer) error {
	major := postgres.DefaultMajor
	if version != "" {
		major = version
		if m := postgres.MajorOf(version); m != "" {
			major = m
		}
	}
	return runPostgres(ctx, s, out, errOut, major, "installing", func(ctx context.Context, ops pgOps) error {
		return ops.Install(ctx, major, extensions...)
	})
}

// updatePostgres updates a major to its newest build, keeping its databases (for
// `flock services update postgres`); "" is the active one.
func updatePostgres(ctx context.Context, s *state, major string, out, errOut io.Writer) error {
	if major == "" {
		if major = postgres.MajorOf(s.cfg.Services.Postgres); major == "" {
			return errors.New("which version? e.g. `flock services update postgres 18`")
		}
	}
	return runPostgres(ctx, s, out, errOut, major, "updating", func(ctx context.Context, ops pgOps) error {
		return ops.Update(ctx, major)
	})
}

// uninstallPostgres removes a major (for `flock services uninstall postgres`); "" is the active one.
func uninstallPostgres(ctx context.Context, s *state, major string, deleteData bool, out, errOut io.Writer) error {
	if major == "" {
		if major = postgres.MajorOf(s.cfg.Services.Postgres); major == "" {
			return errors.New("which version? e.g. `flock services uninstall postgres 18`")
		}
	}
	return runPostgres(ctx, s, out, errOut, major, "uninstalling", func(ctx context.Context, ops pgOps) error {
		return ops.Uninstall(ctx, major, deleteData)
	})
}

// runPostgres runs a PostgreSQL operation through the daemon (or directly when it doesn't run)
// and prints the versions afterwards.
func runPostgres(ctx context.Context, s *state, out, errOut io.Writer, major, what string, do func(ctx context.Context, ops pgOps) error) error {
	ops, err := postgresOps(ctx, s)
	if err != nil {
		return err
	}
	fmt.Fprintf(errOut, "%s PostgreSQL %s…\n", what, major)
	if err := do(ctx, ops); err != nil {
		return err
	}
	v, err := ops.Versions(ctx)
	if err != nil {
		return err
	}
	printPostgresVersions(out, v)
	if m, ok := v.Find(major); ok && m.Active {
		state := "starts with the daemon"
		switch {
		case !v.Enabled:
			state = "switched off — start it in the Flock app"
		case v.Running:
			state = "running"
		}
		fmt.Fprintf(out, "\nPostgreSQL %s on 127.0.0.1:%d (user postgres, no password): %s\n", m.Installed, v.Port, state)
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
	Install(ctx context.Context, major string, extensions ...string) error
	Update(ctx context.Context, major string) error
	Activate(ctx context.Context, major string) error
	Uninstall(ctx context.Context, major string, deleteData bool) error
	Extensions(ctx context.Context, major string) (postgres.MajorExtensions, error)
	// Extension installs, updates or uninstalls (action) an extension of major.
	Extension(ctx context.Context, action, major, name string) error
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
	return directPG{postgres.NewManager(s.root, s.binaries(), &cliPGService{s: s})}, nil
}

type daemonPG struct{ c *api.Client }

func (d daemonPG) Versions(ctx context.Context) (postgres.Versions, error) {
	return d.c.PostgresVersions(ctx)
}
func (d daemonPG) Install(ctx context.Context, major string, extensions ...string) error {
	_, err := d.c.InstallPostgres(ctx, major, extensions...)
	return err
}
func (d daemonPG) Extensions(ctx context.Context, major string) (postgres.MajorExtensions, error) {
	return d.c.PostgresExtensions(ctx, major)
}
func (d daemonPG) Extension(ctx context.Context, action, major, name string) error {
	var err error
	switch action {
	case "install":
		_, err = d.c.InstallPostgresExtension(ctx, major, name)
	case "update":
		_, err = d.c.UpdatePostgresExtension(ctx, major, name)
	case "uninstall":
		_, err = d.c.UninstallPostgresExtension(ctx, major, name)
	}
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
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_ = d.m.Check(ctx) // reported in check_error
	return d.m.Versions(), nil
}
func (d directPG) Install(ctx context.Context, major string, extensions ...string) error {
	return d.m.Install(ctx, major, extensions...)
}
func (d directPG) Extensions(ctx context.Context, major string) (postgres.MajorExtensions, error) {
	if _, err := postgres.EnsureCustomDir(d.m.Installer.Root, major); err != nil {
		return postgres.MajorExtensions{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_ = d.m.Check(ctx)
	return d.m.Extensions(major)
}
func (d directPG) Extension(ctx context.Context, action, major, name string) error {
	switch action {
	case "install":
		return d.m.InstallExtension(ctx, major, name)
	case "update":
		return d.m.UpdateExtension(ctx, major, name)
	case "uninstall":
		return d.m.UninstallExtension(ctx, major, name)
	}
	return nil
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
func (c *cliPGService) Reload() error { return nil } // the daemon applies config.json when it starts

// activeExtensions names the extensions of the active PostgreSQL major, for
// `flock services install postgres <major> --with-extensions`.
func activeExtensions(s *state) []string {
	major := postgres.MajorOf(s.cfg.Services.Postgres)
	if major == "" {
		return nil
	}
	return postgres.InstalledExtensions(s.root, major)
}

// postgresExtensions runs `flock services extensions postgres [action] [extension]`: action is
// list (the default), install, update, uninstall or dir; major "" is the active one.
func postgresExtensions(cmd *cobra.Command, action, name, major string, open bool) error {
	s, err := load()
	if err != nil {
		return err
	}
	if major == "" {
		if major = postgres.MajorOf(s.cfg.Services.Postgres); major == "" {
			return errors.New("which version? e.g. `--major 18`, or install PostgreSQL first: `flock services install postgres`")
		}
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()
	ops, err := postgresOps(ctx, s)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	switch action {
	case "", "list":
	case "pg-config":
		// The build of a major changes with every update, so builds use this rather than a path.
		ids, err := postgres.InstalledIDs(s.root)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if postgres.MajorOf(id) == major {
				fmt.Fprintln(out, filepath.Join(postgres.BinDir(s.root, id), "pg_config"))
				return nil
			}
		}
		return fmt.Errorf("PostgreSQL %s is not installed", major)
	case "dir":
		v, err := ops.Extensions(ctx, major)
		if err != nil {
			return err
		}
		fmt.Fprintln(out, v.CustomDir)
		if open {
			return openFile(v.CustomDir)
		}
		return nil
	case "install", "update", "uninstall":
		if name == "" {
			return fmt.Errorf("which extension? e.g. `flock services extensions postgres %s postgis`", action)
		}
		e, ok := postgres.FindExtension(strings.ToLower(name))
		if !ok {
			return fmt.Errorf("unknown extension %q: %s", name, extensionNames())
		}
		verb := map[string]string{"install": "installing", "update": "updating", "uninstall": "uninstalling"}[action]
		fmt.Fprintf(cmd.ErrOrStderr(), "%s %s for PostgreSQL %s…\n", verb, e.Title, major)
		if err := ops.Extension(ctx, action, major, e.Name); err != nil {
			return err
		}
		if action == "install" {
			fmt.Fprintf(cmd.ErrOrStderr(), "Run CREATE EXTENSION %s; in your database to use it.\n", e.Creates)
		}
	default:
		return fmt.Errorf("unknown action %q: use list, install, update, uninstall, dir or pg-config", action)
	}

	v, err := ops.Extensions(ctx, major)
	if err != nil {
		return err
	}
	printPostgresExtensions(out, v)
	return nil
}

func extensionNames() string {
	names := make([]string, len(postgres.Extensions))
	for i, e := range postgres.Extensions {
		names[i] = e.Name
	}
	return strings.Join(names, ", ")
}

func printPostgresExtensions(out io.Writer, v postgres.MajorExtensions) {
	fmt.Fprintf(out, "PostgreSQL %s extensions:\n", v.Major)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tINSTALLED\tLATEST\tCREATE EXTENSION\tLICENSE\t")
	for _, e := range v.Extensions {
		installed, latest := dash(e.Installed), dash(e.Latest)
		switch {
		case !e.Supported:
			latest = "not available for " + v.Major
		case e.Update:
			latest += " (update)"
		case e.Discontinued:
			latest += " (no longer updated for " + v.Major + ")"
		}
		if e.Busy != "" {
			installed += " (" + e.Busy + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t\n", e.Name, installed, latest, e.Creates, e.License)
	}
	_ = tw.Flush()
	fmt.Fprintf(out, "\nYour own extensions: %s\n", v.CustomDir)
	if len(v.Custom) > 0 {
		tw = tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, x := range v.Custom {
			state := "CREATE EXTENSION " + x.Name + ";"
			if !x.Linked {
				state = x.Problem
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t\n", x.Name, dash(x.Version), state)
		}
		_ = tw.Flush()
	}
}
