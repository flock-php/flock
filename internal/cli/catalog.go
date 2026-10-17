package cli

import (
	"cmp"
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
	"flock/internal/catalog"
	"flock/internal/postgres"
)

func newServicesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "List, install and remove PostgreSQL, MySQL, MariaDB, MongoDB, Valkey, Redis (macOS), Meilisearch, Typesense and RustFS",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			client := s.client()
			c, err := client.Catalog(cmd.Context())
			if err != nil {
				return err
			}
			pg, err := client.PostgresVersions(cmd.Context())
			if err != nil {
				return err
			}
			printCatalog(cmd.OutOrStdout(), pg, c)
			return nil
		},
	}

	var port int
	install := &cobra.Command{
		Use:   "install <service> [version] [--port <port>]",
		Short: "Install a service (the newest release of a version line, e.g. `mysql 8.4` or `postgres 17`) and start it",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			line := ""
			if len(args) == 2 {
				line = args[1]
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			if isPostgres(args[0]) {
				if port != 0 {
					return errors.New("--port doesn't apply to PostgreSQL; change its port in the Flock app's settings")
				}
				return installPostgres(ctx, s, line, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			return installCatalogService(ctx, s, args[0], line, port, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	install.Flags().IntVar(&port, "port", 0, "port to listen on (default: the usual one, or the next free one)")

	var deleteData bool
	uninstall := &cobra.Command{
		Use:   "uninstall <service> [version] [--delete-data]",
		Short: "Remove a version of a service (the active one unless given); its data stays unless --delete-data",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			line := ""
			if len(args) == 2 {
				line = args[1]
			}
			if isPostgres(args[0]) {
				// The daemon finishes an operation it started even when the CLI is interrupted.
				ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
				defer stop()
				return uninstallPostgres(ctx, s, line, deleteData, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			client := s.client()
			if line == "" {
				c, err := client.Catalog(cmd.Context())
				if err != nil {
					return err
				}
				if e, _ := c.Find(args[0]); e.Line != "" {
					line = e.Line
				} else {
					return fmt.Errorf("which version? e.g. `flock services uninstall %s <version>`", args[0])
				}
			}
			c, err := client.UninstallService(cmd.Context(), args[0], line, deleteData)
			if err != nil {
				return err
			}
			e, _ := c.Find(args[0])
			cmd.Printf("%s %s uninstalled\n", cmp.Or(e.Name, args[0]), line)
			if e.Installed != "" {
				cmd.Printf("%s %s runs on port %d\n", e.Name, e.Installed, e.Port)
			}
			return nil
		},
	}
	uninstall.Flags().BoolVar(&deleteData, "delete-data", false, "also delete that version's data (PostgreSQL: its databases)")

	use := &cobra.Command{
		Use:   "use <service> <version>",
		Short: "Run another installed version of a service on its port (each version keeps its own data)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if isPostgres(args[0]) {
				return postgresOperation(cmd, args[1], "switching to", func(ctx context.Context, ops pgOps) error {
					return ops.Activate(ctx, args[1])
				})
			}
			s, err := load()
			if err != nil {
				return err
			}
			c, err := s.client().ActivateService(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			e, _ := c.Find(args[0])
			cmd.Printf("%s %s runs on port %d\n", e.Name, e.Installed, e.Port)
			return nil
		},
	}

	cmd.AddCommand(install, uninstall, use)
	return cmd
}

func printCatalog(out io.Writer, pg postgres.Versions, c api.Catalog) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSTATE\tVERSIONS")
	fmt.Fprintf(w, "postgres\t%s\t%s\n", postgresState(pg), strings.Join(postgresMajors(pg), ", "))
	for _, e := range c.Services {
		state := "not installed"
		switch {
		case !e.Available:
			state = "not available: " + e.Unavailable
		case e.Job != nil && e.Job.State == "running":
			state = "installing…"
		case e.Installed != "":
			state = fmt.Sprintf("%s on port %d", e.Installed, e.Port)
			if e.Update {
				state += fmt.Sprintf(" (%s available)", e.Latest)
			}
		}
		var lines []string
		for _, l := range e.Lines {
			lines = append(lines, l.ID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", e.ID, state, strings.Join(lines, ", "))
	}
	_ = w.Flush()
}

// postgresState is PostgreSQL's STATE in `flock services`: the active version, like a catalog
// service's, with the other installed majors (`flock postgres list` shows them in full).
func postgresState(v postgres.Versions) string {
	var active postgres.MajorVersion
	var others []string
	for _, m := range v.Majors {
		switch {
		case m.Busy != "":
			return m.Busy + "…"
		case m.Active:
			active = m
		case m.Installed != "":
			others = append(others, m.Installed)
		}
	}
	if active.Installed == "" {
		if len(others) > 0 {
			return strings.Join(others, ", ") + " installed, none active"
		}
		return "not installed"
	}
	state := fmt.Sprintf("%s on port %d", active.Installed, v.Port)
	if active.Update {
		state += fmt.Sprintf(" (%s available)", active.Latest)
	}
	if len(others) > 0 {
		state += "; also " + strings.Join(others, ", ")
	}
	return state
}

func postgresMajors(v postgres.Versions) []string {
	var majors []string
	for _, m := range v.Majors {
		majors = append(majors, m.Major)
	}
	return majors
}

// installCatalogService starts an install in the daemon and follows it until it finishes.
func installCatalogService(ctx context.Context, s *state, id, line string, port int, out, errOut io.Writer) error {
	client := s.client()
	c, err := client.InstallService(ctx, id, line, port)
	if err != nil {
		return err
	}
	p := newProgress(errOut)
	defer p.clear()
	for {
		e, ok := c.Find(id)
		if !ok {
			return fmt.Errorf("unknown service %q", id)
		}
		switch {
		case e.Job == nil:
			p.clear()
			for _, l := range e.Lines {
				if l.ID == line && l.Installed && !l.Active {
					fmt.Fprintf(out, "%s %s installed next to %s, which keeps running; switch with `flock services use %s %s`\n", e.Name, l.Version, e.Installed, id, line)
					return nil
				}
			}
			fmt.Fprintf(out, "%s %s on 127.0.0.1:%d\n", e.Name, e.Installed, e.Port)
			return nil
		case e.Job.State == "failed":
			p.clear()
			return fmt.Errorf("installing %s failed: %s (the log is %s)", e.Name, e.Job.Error, filepath.Join(s.root, "log", id+"-install.log"))
		}
		p.set(jobText(e))

		select {
		case <-ctx.Done():
			// Interrupted: cancel the install in the daemon too.
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = client.CancelService(cancelCtx, id)
			return errors.New("canceled")
		case <-time.After(time.Second):
		}
		if c, err = client.Catalog(ctx); err != nil && ctx.Err() == nil {
			return err
		}
	}
}

func jobText(e catalog.Entry) string {
	target := e.Name
	if e.Job.Version != "" {
		target += " " + e.Job.Version
	}
	switch e.Job.Phase {
	case "downloading":
		if e.Job.Progress >= 0 {
			return fmt.Sprintf("downloading %s (%d%%)…", target, int(e.Job.Progress*100))
		}
		return fmt.Sprintf("downloading %s…", target)
	}
	return fmt.Sprintf("installing %s…", target)
}
