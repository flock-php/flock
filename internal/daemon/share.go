package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/project"
	"flock/internal/share"
)

// Shares implements api.Backend.
func (d *daemon) Shares() *share.Manager { return d.shares }

// StartShare implements api.Backend: it checks that the site can be shared safely, then starts the
// share, building the assets first when they need it.
func (d *daemon) StartShare(name string, allow []string, debug bool) error {
	d.mu.Lock()
	cfg, sites := d.cfg, d.sites
	d.mu.Unlock()
	if cfg == nil {
		return errors.New("the daemon has no configuration yet")
	}
	site, ok := cfg.FindSite(name)
	if !ok {
		return fmt.Errorf("no site named %s", name)
	}
	for _, s := range sites {
		if s.Name == name && s.URL == "" {
			return fmt.Errorf("%s isn't served: %s", name, s.Problem)
		}
	}
	req := share.Request{Site: name, Allow: allow, Debug: debug}
	if !debug && share.ConfigCached(site.Path) {
		req.Warning = "The app's config is cached, so visitors get its own debug setting (APP_DEBUG). Run `php artisan config:clear` and share again to turn debug mode off for them."
	}
	if share.UsesVite(site.Path) {
		req.BuildFirst = share.Stale(site.Path)
		// The project's build scripts are its own code: they never run with root's rights.
		switch {
		case !runningAsRoot():
			req.Build = d.buildAssets(cfg, site)
		case req.BuildFirst:
			return errors.New("the assets need building, and Flock doesn't run the project's build scripts as root — run `npm run build` first")
		}
	}
	return d.shares.Start(req)
}

// buildAssets runs `npm run build` in a site with the Node.js version it uses.
func (d *daemon) buildAssets(cfg *config.Config, site config.Site) func(context.Context, io.Writer) error {
	return func(ctx context.Context, log io.Writer) error {
		spec := project.NodeFor(cfg, &site, site.Path).Version
		_, version, err := node.Resolve(d.root, spec)
		if err != nil {
			return err
		}
		return node.RunNpm(ctx, d.root, version, site.Path, log, "run", "build")
	}
}

func runningAsRoot() bool {
	return runtime.GOOS != "windows" && os.Geteuid() == 0
}

// withShares adds each site's share to the statuses.
func (d *daemon) withShares(sites []api.SiteStatus) []api.SiteStatus {
	if d.shares == nil {
		return sites
	}
	for i := range sites {
		sites[i].Share = d.shares.Get(sites[i].Name)
	}
	return sites
}
