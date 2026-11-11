package cli

import (
	"cmp"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
)

func newProxyCommand() *cobra.Command {
	var (
		secure                    bool
		command, dir, stopCommand string
	)

	cmd := &cobra.Command{
		Use:   "proxy <name> <port|host:port|url>",
		Short: "Serve <name>.test from a server of its own, e.g. a dashboard, an object store or a Docker container",
		Long: "Serve <name>.test (and its subdomains) from a server of its own, e.g. a dashboard, an object store, " +
			"a Docker container or a Go or Python app. Requests pass through with their Host header and WebSocket " +
			"upgrades (hot reload) included.\n\n" +
			"A port means 127.0.0.1: `flock proxy app 3000` serves http://app.test from http://127.0.0.1:3000.\n\n" +
			"With --command, Flock can start and stop the server too (`flock proxy start|stop <name>`, or the app): " +
			"it runs the command in the current folder (or --dir), and `flock logs <name>` shows its output. A command " +
			"that starts the server in the background and exits, like `docker compose up -d`, needs --stop-command. " +
			"An empty --command removes it.",
		Example: "  flock proxy softpack 3000\n  flock proxy s3 127.0.0.1:9000 --secure\n  flock proxy api https://127.0.0.1:8443\n" +
			"  flock proxy admin 8000 --command \"python manage.py runserver 8000\"\n" +
			"  flock proxy s3 9000 --command \"docker compose up -d\" --stop-command \"docker compose stop\"\n" +
			"  flock proxy start admin",
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
			p := config.Proxy{Name: config.SiteName(args[0])}
			if existing, ok := s.cfg.FindProxy(p.Name); ok {
				p = existing
			}
			p.Upstream = upstream
			if cmd.Flags().Changed("secure") {
				p.Secured = secure
			}
			if err := applyCommandFlags(cmd, &p, command, dir, stopCommand); err != nil {
				return err
			}
			if err := s.cfg.UpsertProxy(p); err != nil {
				return err
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("proxied %s → %s\n", proxyURL(s.cfg, p), upstream)
			switch {
			case p.Command != "":
				cmd.Printf("start its server with `flock proxy start %s`\n", p.Name)
			case !answers(upstream):
				cmd.Printf("hint: nothing answers at %s yet — start the server there\n", upstream)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&secure, "secure", false, "serve over HTTPS")
	cmd.Flags().StringVar(&command, "command", "", "the command that starts the server, so Flock can start and stop it")
	cmd.Flags().StringVar(&dir, "dir", "", "the folder the command runs in (default: the current folder)")
	cmd.Flags().StringVar(&stopCommand, "stop-command", "", "for a command that starts the server in the background: the command that stops it")

	// `flock proxy start <name>` is the subcommand, not a proxy named start.
	cmd.AddCommand(newProxyServerCommand(true), newProxyServerCommand(false))
	return cmd
}

// applyCommandFlags sets the command flags given on p. A new command runs in the current folder
// unless --dir names one.
func applyCommandFlags(cmd *cobra.Command, p *config.Proxy, command, dir, stopCommand string) error {
	flags := cmd.Flags()
	if flags.Changed("command") {
		p.Command = strings.TrimSpace(command)
		if p.Command != "" && p.Dir == "" && !flags.Changed("dir") {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			p.Dir = cwd
		}
	}
	if flags.Changed("dir") {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return fmt.Errorf("%s is not a folder", dir)
		}
		p.Dir = abs
	}
	if flags.Changed("stop-command") {
		p.StopCommand = strings.TrimSpace(stopCommand)
	}
	if flags.Changed("command") && p.Command == "" {
		p.Dir, p.StopCommand = "", ""
	}
	return nil
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
	return cmd
}

// newProxyServerCommand is `flock proxy start` (start) or `flock proxy stop`.
func newProxyServerCommand(start bool) *cobra.Command {
	use, short := "stop <name>", "Stop the server Flock started for a proxy"
	if start {
		use, short = "start <name>", "Start a proxy's server with its command"
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
			name := config.SiteName(args[0])
			client := s.client()
			if start {
				if _, err := client.StartProxy(cmd.Context(), name); err != nil {
					return err
				}
			} else if _, err := client.StopProxy(cmd.Context(), name); err != nil {
				return err
			}
			// Wait a little for the server (or a background stop command), to say how it went.
			return waitForProxy(cmd, client, name, start, proxyWait)
		},
	}
}

// proxyWait is how long `flock proxy start|stop` waits to say how it went. Tests shorten it.
var proxyWait = 30 * time.Second

// waitForProxy waits until a proxy's server runs (start) or stopped, and says so, or why not.
// It gives up after wait, and when the proxy or its command is gone.
func waitForProxy(cmd *cobra.Command, client *api.Client, name string, start bool, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		st, err := client.Status(cmd.Context())
		if err != nil {
			return err
		}
		var command *api.ProxyCommand
		var url string
		for _, p := range st.Proxies {
			if p.Name == name {
				command, url = p.Command, p.URL
			}
		}
		switch {
		case command == nil:
			return fmt.Errorf("%s no longer has a command", name)
		case command.State == api.ProxyFailed:
			return fmt.Errorf("%s: %s (see `flock logs %s`)", name, command.Problem, name)
		case start && command.State == api.ProxyRunning:
			cmd.Printf("started %s: %s\n", name, url)
			return nil
		case !start && command.State == api.ProxyStopped:
			cmd.Printf("stopped %s\n", name)
			return nil
		case time.Now().After(deadline):
			verb := "stopping"
			if start {
				verb = "starting"
			}
			cmd.Printf("%s is still %s — see `flock logs %s`\n", name, verb, name)
			return nil
		}
		time.Sleep(300 * time.Millisecond)
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
			if c := p.Command; c != nil && c.State != api.ProxyRunning && c.State != api.ProxyStopped {
				server = c.State
				if c.Problem != "" {
					server += ": " + c.Problem
				}
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
