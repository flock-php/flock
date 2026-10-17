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
	"flock/internal/catalog"
)

func newServicesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "services",
		Short: "List, install and remove MySQL, MariaDB, MongoDB, Valkey, Redis (macOS), Meilisearch, Typesense and RustFS",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			c, err := s.client().Catalog(cmd.Context())
			if err != nil {
				return err
			}
			printCatalog(cmd.OutOrStdout(), c)
			return nil
		},
	}

	var port int
	install := &cobra.Command{
		Use:   "install <service> [version] [--port <port>]",
		Short: "Install a service (the newest release of a version line, e.g. `mysql 8.4`) and start it",
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
			return installCatalogService(ctx, s, args[0], line, port, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	install.Flags().IntVar(&port, "port", 0, "port to listen on (default: the usual one, or the next free one)")

	var deleteData bool
	uninstall := &cobra.Command{
		Use:   "uninstall <service> [--delete-data]",
		Short: "Stop and remove a service; its data stays unless --delete-data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if _, err := s.client().UninstallService(cmd.Context(), args[0], deleteData); err != nil {
				return err
			}
			cmd.Printf("%s uninstalled\n", args[0])
			return nil
		},
	}
	uninstall.Flags().BoolVar(&deleteData, "delete-data", false, "also delete the service's data (every version)")

	cmd.AddCommand(install, uninstall)
	return cmd
}

func printCatalog(out io.Writer, c api.Catalog) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSTATE\tVERSIONS")
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
