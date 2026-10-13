package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the state of the Flock daemon and its services",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			st, err := s.client().Status(cmd.Context())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "daemon    running (pid %d, %s, up %s)\n", st.PID, st.Version, time.Since(st.StartedAt).Round(time.Second))
			switch {
			case st.DNS.Wildcard:
				fmt.Fprintf(out, "dns       *.%s → 127.0.0.1, any subdomain (server on %s)\n", st.DNS.TLD, st.DNS.Addr)
			case st.DNS.TLD != "":
				fmt.Fprintf(out, "dns       site domains and extra domains → 127.0.0.1 (hosts file); other *.%s subdomains don't resolve: %s\n",
					st.DNS.TLD, st.DNS.WildcardProblem)
			}
			fmt.Fprintf(out, "web       http :%d, https :%d\n", st.HTTPPort, st.HTTPSPort)
			fmt.Fprintf(out, "mail      smtp://%s\n", st.Mail)
			if st.Postgres != "" {
				fmt.Fprintf(out, "postgres  %s\n", st.Postgres)
			}
			if st.Redis != "" {
				fmt.Fprintf(out, "redis     %s\n", st.Redis)
			}
			if st.Xdebug {
				fmt.Fprintln(out, "xdebug    on (port 9003, start with XDEBUG_TRIGGER)")
			}
			for _, p := range st.Processes {
				state := "up"
				if !p.Up {
					state = fmt.Sprintf("DOWN (restarted %d×, last error: %s)", p.Restarts, p.LastErr)
				} else if p.Restarts > 0 {
					state = fmt.Sprintf("up (restarted %d×)", p.Restarts)
				}
				fmt.Fprintf(out, "process   %s %s\n", p.Name, state)
			}
			for _, w := range st.Warnings {
				fmt.Fprintf(out, "warning   %s\n", w)
			}

			return nil
		},
	}
}

func newSitesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sites",
		Short: "List linked and parked sites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SITE\tPHP\tURL\tPATH")

			st, err := s.client().Status(cmd.Context())
			if err == nil {
				for _, site := range st.Sites {
					url := site.URL
					if site.Problem != "" {
						url = "⚠ " + site.Problem
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", site.Name, site.PHP, url, site.Path)
				}
				return w.Flush()
			}

			// Daemon down: show what the config says.
			for _, site := range s.cfg.AllSites() {
				scheme := "http"
				if site.Secured {
					scheme = "https"
				}
				fmt.Fprintf(w, "%s\t%s\t%s://%s\t%s\n", site.Name, s.cfg.PHPFor(site), scheme, s.cfg.Domain(site), site.Path)
			}
			return w.Flush()
		},
	}
}
