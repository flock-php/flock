package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
)

// nodeSiteCommands are `flock node serve`, `unserve` and `restart`: a site as a Node.js app, whose
// `npm run dev` Flock runs with the site's Node.js and serves the site from.
func nodeSiteCommands() []*cobra.Command {
	serve := &cobra.Command{
		Use:   "serve [site]",
		Short: "Serve a site as a Node.js app: Flock runs its `npm run dev` with the site's Node.js",
		Long: `Flock runs the site's ` + "`npm run dev -- --port <port>`" + ` (PORT is set too) with the Node.js the
project pins (.flockrc, .nvmrc or .node-version), an isolated one or the default, restarts it when
it exits, and serves the site's domains and HTTPS from it, WebSocket upgrades (hot reload) included.
Its output: ` + "`flock logs node-<site>`" + `. Back to PHP: ` + "`flock node unserve`" + `.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if err := s.cfg.SetServer(&site, config.ServerNode, ""); err != nil {
				return err
			}
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return waitForNodeServer(ctx, cmd, s, site.Name)
		},
	}

	unserve := &cobra.Command{
		Use:   "unserve [site]",
		Short: "Stop a site's dev server and serve the site with PHP again",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if !site.RunsNode() {
				return errors.New(site.Name + " isn't served as a Node.js app")
			}
			if err := s.cfg.SetServer(&site, config.ServerFastCGI, ""); err != nil {
				return err
			}
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("%s is served with %s\n", site.Name, fastCGIName())
			return nil
		},
	}

	restart := &cobra.Command{
		Use:   "restart [site]",
		Short: "Restart a Node.js site's dev server",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args)
			if err != nil {
				return err
			}
			if _, err := s.client().RestartSite(cmd.Context(), site.Name); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return waitForNodeServer(ctx, cmd, s, site.Name)
		},
	}

	return []*cobra.Command{serve, unserve, restart}
}

// waitForNodeServer follows a Node.js site's dev server until it answers or fails (a dev server
// compiles first), for up to a minute.
func waitForNodeServer(ctx context.Context, cmd *cobra.Command, s *state, name string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	said := false
	for {
		st, err := s.client().Status(ctx)
		if errors.Is(err, api.ErrDaemonNotRunning) {
			return nil // commit said so
		}
		if err != nil {
			return err
		}
		for _, x := range st.Sites {
			if x.Name != name || x.NodeServer == nil {
				continue
			}
			ns := x.NodeServer
			switch ns.State {
			case api.NodeRunning:
				cmd.Printf("%s runs `npm run %s` on Node.js %s — %s, log: `flock logs %s`\n", name, ns.Script, ns.Version, x.URL, ns.Log)
				return nil
			case api.NodeFailed, api.NodeUnavailable:
				return errors.New(name + ": " + ns.Problem)
			}
			if !said {
				cmd.Printf("starting `npm run %s` for %s on Node.js %s…\n", ns.Script, name, ns.Version)
				said = true
			}
		}
		select {
		case <-ctx.Done():
			cmd.Printf("%s's dev server is still starting — follow it with `flock logs %s`\n", name, config.NodePortID(name))
			return nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}
