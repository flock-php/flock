package daemon

import (
	"fmt"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/ports"
	"flock/internal/reverb"
	"flock/internal/supervisor"
)

// reverbStatus is a site's Reverb server as status reports it, before its state is known.
func reverbStatus(cfg *config.Config, site config.Site, scheme string) api.SiteReverb {
	port := cfg.Ports.HTTP
	if scheme == "https" {
		port = cfg.Ports.HTTPS
	}
	return api.SiteReverb{Port: site.ReverbPort, Log: config.ReverbPortID(site.Name), Env: reverb.Env(cfg.Domain(site), scheme, port)}
}

// reverbBlocked is the status of an enabled Reverb server that can't run because the site itself
// can't be served (no PHP, no document root), or nil when Reverb is off.
func reverbBlocked(cfg *config.Config, site config.Site, scheme, problem string) *api.SiteReverb {
	if !site.Reverb {
		return nil
	}
	st := reverbStatus(cfg, site, scheme)
	st.State, st.Problem = api.ReverbUnavailable, problem
	return &st
}

// reverbPreflightWarning is the warning for a Reverb server that couldn't be started at all (its
// port is busy, say), or "". Warnings make the daemon retry its reload, so Reverb starts once the
// port is free; without one an otherwise healthy daemon never tried again.
func reverbPreflightWarning(site string, st api.SiteReverb) string {
	if st.State != api.ReverbFailed {
		return ""
	}
	return fmt.Sprintf("reverb: %s: %s", site, st.Problem)
}

// reverbSite prepares a site's Reverb server: its process, when it can run, and its state.
func (d *daemon) reverbSite(cfg *config.Config, site config.Site, minor, scheme string, extraEnv []string) (*supervisor.Spec, api.SiteReverb) {
	name := config.ReverbPortID(site.Name)
	st := reverbStatus(cfg, site, scheme)
	fail := func(state, format string, args ...any) (*supervisor.Spec, api.SiteReverb) {
		st.State, st.Problem = state, fmt.Sprintf(format, args...)
		return nil, st
	}

	if !reverb.Installed(site.Path) {
		return fail(api.ReverbUnavailable, "Laravel Reverb isn't installed in %s — run `php artisan install:broadcasting --reverb`", site.Path)
	}
	if other, taken := cfg.UsedPorts("")[site.ReverbPort]; site.ReverbPort == 0 || taken && other != name {
		return fail(api.ReverbFailed, "port %d is also configured for %s — turn Reverb off and on again for a new port", site.ReverbPort, serviceName(other))
	}
	d.mu.Lock()
	restart := d.restarts[site.Name]
	d.mu.Unlock()
	spec := reverb.Spec(d.root, reverb.Site{Name: site.Name, Path: site.Path, Minor: minor, Port: site.ReverbPort, Env: extraEnv, Restart: restart})
	if !d.procUp(spec.Name) && !ports.Free("127.0.0.1", site.ReverbPort) {
		return fail(api.ReverbFailed, "%s", ports.Describe(site.ReverbPort))
	}
	return &spec, st
}
