package cli

import (
	"cmp"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
)

func newProxyCommand() *cobra.Command {
	var secure bool
	var command, dir, stopCommand string

	cmd := &cobra.Command{
		Use:   "proxy <name> <port|host:port|url>",
		Short: "Serve <name>.test from a server of its own, e.g. a Node.js dev server on a port",
		Long: "Serve <name>.test (and its subdomains) from a server of its own instead of PHP, e.g. a Next.js, " +
			"Nuxt or Vite dev server, an object store or a dashboard. Requests pass through with their Host header " +
			"and WebSocket upgrades (hot reload) included.\n\n" +
			"A port means 127.0.0.1: `flock proxy app 3000` serves http://app.test from http://127.0.0.1:3000.\n\n" +
			"--command gives the proxy the command that starts its server, run through the shell in --dir (the current " +
			"folder by default) with Flock's PHP and Node.js on PATH and PORT set: `flock proxies start <name>` runs it and " +
			"keeps it running, `flock proxies stop <name>` stops it, and its output is in `flock logs proxy-<name>`. " +
			"For a command that starts a server in the background and exits, --stop-command stops that server. " +
			"--command \"\" removes them.",
		Example: "  flock proxy softpack 3000\n  flock proxy softpack 3000 --command \"npm run dev\"\n" +
			"  flock proxy s3 127.0.0.1:9000 --secure --command \"docker compose up -d\" --stop-command \"docker compose stop\"\n" +
			"  flock proxy api https://127.0.0.1:8443",
		Args: cobra.ExactArgs(2),
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
			if existing, ok := s.cfg.FindProxy(p.Name); ok {
				p = existing
				p.Upstream = upstream
				if cmd.Flags().Changed("secure") {
					p.Secured = secure
				}
			}
			if cmd.Flags().Changed("command") {
				p.Command = command
				if command == "" {
					p.StopCommand = ""
				}
			}
			if cmd.Flags().Changed("stop-command") {
				p.StopCommand = stopCommand
			}
			if cmd.Flags().Changed("dir") || (p.Command != "" && p.Dir == "") {
				if p.Dir, err = filepath.Abs(dir); err != nil {
					return err
				}
			}
			if err := s.cfg.UpsertProxy(p); err != nil {
				return err
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("proxied %s → %s\n", proxyURL(s.cfg, p), upstream)
			if p.Command != "" {
				cmd.Printf("start its server (`%s` in %s) with `flock proxies start %s`\n", p.Command, p.Dir, p.Name)
			} else if !answers(upstream) {
				cmd.Printf("hint: nothing answers at %s yet — start the server there\n", upstream)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&secure, "secure", false, "serve over HTTPS")
	cmd.Flags().StringVar(&command, "command", "", "the command that starts the proxy's server, e.g. \"npm run dev\"")
	cmd.Flags().StringVar(&dir, "dir", ".", "the folder the command runs in")
	cmd.Flags().StringVar(&stopCommand, "stop-command", "", "the command that stops a server the command starts in the background")

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
	cmd := &cobra.Command{
		Use:   "proxies",
		Short: "List proxies, or start and stop their servers",
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
	cmd.AddCommand(proxyServerCommand(true), proxyServerCommand(false))
	return cmd
}

// proxyServerCommand is `flock proxies start` or `stop`: a proxy's command.
func proxyServerCommand(start bool) *cobra.Command {
	use, short := "start <name>", "Run a proxy's command, which starts its server, and keep it running"
	if !start {
		use, short = "stop <name>", "Stop a proxy's command, or run its stop command"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			act := s.client().StopProxy
			if start {
				act = s.client().StartProxy
			}
			st, err := act(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, p := range st.Proxies {
				if p.Name == args[0] && p.Command != nil {
					if start {
						cmd.Printf("started `%s` in %s — its output: flock logs %s\n", p.Command.Command, p.Command.Dir, p.Command.Log)
					} else {
						cmd.Printf("stopped %s's server\n", p.Name)
					}
				}
			}
			return nil
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
			if c := p.Command; c != nil && c.State != api.ProxyStopped && c.State != api.ProxyRunning {
				server = c.State + " (" + c.Command + ")"
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
