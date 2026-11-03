package cli

import (
	"cmp"
	"fmt"
	"net"
	"net/url"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
)

func newProxyCommand() *cobra.Command {
	var secure bool

	cmd := &cobra.Command{
		Use:   "proxy <name> <port|host:port|url>",
		Short: "Serve <name>.test from a server of its own, e.g. a Node.js dev server on a port",
		Long: "Serve <name>.test (and its subdomains) from a server of its own instead of PHP, e.g. a Next.js, " +
			"Nuxt or Vite dev server, an object store or a dashboard. Requests pass through with their Host header " +
			"and WebSocket upgrades (hot reload) included.\n\n" +
			"A port means 127.0.0.1: `flock proxy app 3000` serves http://app.test from http://127.0.0.1:3000.",
		Example: "  flock proxy softpack 3000\n  flock proxy s3 127.0.0.1:9000 --secure\n  flock proxy api https://127.0.0.1:8443",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			upstream, err := config.NormalizeUpstream(args[1])
			if err != nil {
				return err
			}
			p := config.Proxy{Name: config.SiteName(args[0]), Upstream: upstream, Secured: secure}
			if existing, ok := s.cfg.FindProxy(p.Name); ok && !cmd.Flags().Changed("secure") {
				p.Secured = existing.Secured
			}
			if err := s.cfg.UpsertProxy(p); err != nil {
				return err
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("proxied %s → %s\n", proxyURL(s.cfg, p), upstream)
			if !answers(upstream) {
				cmd.Printf("hint: nothing answers at %s yet — start the server there\n", upstream)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&secure, "secure", false, "serve over HTTPS")

	return cmd
}

func newUnproxyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "unproxy <name>",
		Short: "Stop serving a proxy",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if !s.cfg.RemoveProxy(args[0]) {
				return fmt.Errorf("no proxy named %q", args[0])
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("unproxied %s\n", args[0])
			return nil
		},
	}
}

func newProxiesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "proxies",
		Short: "List proxies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROXY\tURL\tUPSTREAM\tSERVER")
			var live *api.Status
			if st, err := s.client().Status(cmd.Context()); err == nil {
				live = &st
			}
			for _, p := range proxyRows(live, s.cfg) {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.name, p.url, p.upstream, p.server)
			}
			return w.Flush()
		},
	}
}

type proxyRow struct{ name, url, upstream, server string }

// proxyRows are the proxies as the daemon serves them (st), or as cfg has them while it's down (nil st).
func proxyRows(st *api.Status, cfg *config.Config) []proxyRow {
	var rows []proxyRow
	if st != nil {
		for _, p := range st.Proxies {
			u := p.URL
			if p.Problem != "" {
				u = "⚠ " + p.Problem
			}
			server := "not running"
			if p.Running {
				server = cmp.Or(p.Server, "running")
			}
			rows = append(rows, proxyRow{p.Name, u, p.Upstream, server})
		}
		return rows
	}
	for _, p := range cfg.Proxies {
		rows = append(rows, proxyRow{p.Name, proxyURL(cfg, p), p.Upstream, "-"})
	}
	return rows
}

func proxyURL(cfg *config.Config, p config.Proxy) string {
	scheme := "http"
	if p.Secured {
		scheme = "https"
	}
	return scheme + "://" + cfg.ProxyDomain(p)
}

// answers reports whether something accepts connections at upstream.
func answers(upstream string) bool {
	u, err := url.Parse(upstream)
	if err != nil {
		return false
	}
	host := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	conn, err := net.DialTimeout("tcp", host, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
