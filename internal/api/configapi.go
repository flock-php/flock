package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"flock/internal/binaries"
	"flock/internal/capture"
	"flock/internal/config"
	"flock/internal/frankenphp"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/ports"
	"flock/internal/project"
)

// Service is one background service as shown in the Services settings.
type Service struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
	// Available is false for a service Flock can't run on this platform; Unavailable says why.
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable,omitempty"`
	Version     string `json:"version"`
	Port        int    `json:"port"`
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
	Running     bool   `json:"running"`
	Stoppable   bool   `json:"stoppable"`
	Env         string `json:"env"`               // .env snippet to connect a Laravel app
	Log         string `json:"log"`               // name for /v1/logs/services/{name}
	URL         string `json:"url"`               // connection URL (e.g. for TablePlus)
	Problem     string `json:"problem,omitempty"` // why it isn't working, when that isn't simply "stopped"
}

// ConfigView is config.json plus installed versions, for the settings UI.
type ConfigView struct {
	TLD               string       `json:"tld"`
	DefaultPHP        string       `json:"default_php"`
	DefaultNode       string       `json:"default_node"`
	Xdebug            bool         `json:"xdebug"`
	XdebugBreakpoints bool         `json:"xdebug_breakpoints"`
	Dumps             bool         `json:"dumps"`
	CaptureOff        []string     `json:"capture_off"`
	DumpsInPage       bool         `json:"dumps_in_page"`
	Channel           string       `json:"channel"` // "stable", "beta" or "experimental"
	Root              string       `json:"root"`
	Ports             config.Ports `json:"ports"`
	Parked            []string     `json:"parked"`
	PHP               []PHPVersion `json:"php"`
	Node              []string     `json:"node"`
}

// PHPVersion is one installed PHP minor version.
type PHPVersion struct {
	Version         string `json:"version"`
	FullVersion     string `json:"full_version"`
	Ini             string `json:"ini"`
	XdebugAvailable bool   `json:"xdebug_available"`
}

// errConflict and errInvalid map to HTTP 409 and 400.
var (
	errConflict = errors.New("conflict")
	errInvalid  = errors.New("invalid request")
)

func statusFor(err error) int {
	switch {
	case errors.Is(err, errConflict):
		return http.StatusConflict
	case errors.Is(err, errInvalid):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, args...))
}

// pinSite pins a version, or with tool "root" the document root, into the site's project (see
// project.PinSite and project.PinSiteRoot), storing the site when its isolation was lifted.
func pinSite(cfg *config.Config, site config.Site, tool, version string) error {
	var lifted bool
	var err error
	if tool == project.Root {
		_, lifted, err = project.PinSiteRoot(&site, version)
	} else {
		_, lifted, err = project.PinSite(cfg, &site, tool, version)
	}
	if err != nil {
		if errors.Is(err, project.ErrInvalid) {
			return invalid("%v", err)
		}
		return err
	}
	if lifted {
		if err := cfg.Upsert(site); err != nil {
			return invalid("%v", err)
		}
	}
	return nil
}

// setServer switches a site's server. Switching to FrankenPHP retries a failed install of the build
// for the site's PHP version; the reload starts installing a missing one.
func setServer(b Backend, cfg *config.Config, site *config.Site, server, worker string) error {
	m := b.FrankenPHP()
	if server == config.ServerFrankenPHP {
		if m == nil || !m.Available() {
			return invalid("FrankenPHP isn't available for this platform")
		}
		minor := project.PHPFor(cfg, site, site.Path).Version
		if !frankenphp.Supports(minor) {
			return invalid("FrankenPHP needs PHP %s or newer (%s uses %s)", frankenphp.MinimumMinor, site.Name, minor)
		}
		m.Dismiss(minor)
	}
	if err := cfg.SetServer(site, server, worker); err != nil {
		return invalid("%v", err)
	}
	return nil
}

func applySettings(root string, cfg *config.Config, s Settings) error {
	if s.Xdebug != nil {
		cfg.Xdebug = *s.Xdebug
	}
	if s.XdebugBreakpoints != nil {
		cfg.XdebugBreakpoints = *s.XdebugBreakpoints
	}
	if s.Dumps != nil {
		cfg.Dumps = *s.Dumps
	}
	if s.CaptureOff != nil {
		if err := capture.Valid(*s.CaptureOff); err != nil {
			return invalid("%v", err)
		}
		cfg.CaptureOff = nil
		for _, k := range capture.Kinds { // known kinds once each, in a stable order
			if slices.Contains(*s.CaptureOff, k) {
				cfg.CaptureOff = append(cfg.CaptureOff, k)
			}
		}
	}
	if s.DumpsInPage != nil {
		cfg.DumpsInPage = *s.DumpsInPage
	}
	if s.DefaultPHP != nil {
		installed, _ := php.Installed(root)
		if !slices.Contains(installed, *s.DefaultPHP) {
			return invalid("PHP %s is not installed", *s.DefaultPHP)
		}
		cfg.DefaultPHP = *s.DefaultPHP
	}
	if s.Channel != nil {
		ch, err := binaries.ParseChannel(*s.Channel)
		if err != nil {
			return invalid("%v", err)
		}
		cfg.Channel = string(ch)
		if ch == binaries.Stable {
			cfg.Channel = ""
		}
	}
	if s.DefaultNode != nil {
		if *s.DefaultNode == "" {
			return invalid("default_node must not be empty")
		}
		cfg.DefaultNode = *s.DefaultNode
	}
	if s.TLD != nil {
		// Validate before SetTLD: a rejected TLD must not migrate the sites' aliases
		// either, and this closure runs inside b.Mutate — returning the error here is
		// what leaves config.json untouched. ValidateTLD's warning (for "local", "localhost",
		// "alt") is dropped: the apps show it from the live tld/check verdict, and the daemon
		// reports the .local caveat through Status.Warnings.
		tld := config.NormalizeTLD(*s.TLD)
		if _, err := config.ValidateTLD(tld); err != nil {
			return invalid("%v", err)
		}
		// The landing probe before SetTLD, the CLI's `flock tld` rule (config.
		// TLDMoveCollision): a global move is refused when a host ends up claimed twice
		// by claims the switch creates. This closure runs inside b.Mutate, so returning
		// the error leaves config.json untouched.
		if col := cfg.TLDMoveCollision(tld); col != nil {
			if col.Self != "" {
				return invalid("%s is claimed twice by %s: the alias %s that follows the site lands on the pinned alias %s — remove one first", col.Host, col.Self, col.Moved, col.Host)
			}
			return invalid("%s is already served by %s: the switch to .%s can't move %s there", col.Host, col.Holder, tld, col.Mover)
		}
		cfg.SetTLD(tld)
	}
	return nil
}

func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v); err != nil {
		return invalid("%v", err)
	}
	return nil
}

// siteTLD normalizes a requested site suffix: "" is inherit (stored as ""), anything else is
// validated and stored sanitized. A rejected one is an invalid() error: callers run it inside
// b.Mutate, so returning it leaves config.json untouched.
func siteTLD(raw string) (string, error) {
	tld := config.NormalizeTLD(raw)
	if tld == "" {
		return "", nil // "" (like an absent suffix) is inherit
	}
	if _, err := config.ValidateTLD(tld); err != nil {
		return "", invalid("%v", err)
	}
	return tld, nil
}

// siteHostClash reports why the site — in the state given, its TLD override as proposed —
// lands on a host Flock already serves: a suffix change moves the site's domain and its
// inherited aliases, and any of them (the site's full set of effective hosts, pinned
// aliases included) can land on a proxy's host, another site's domain, or another site's
// alias. This is the config-side check the CLI's `flock site tld` runs
// (state.siteHostsFree), so the API refuses exactly the collisions the CLI does — not just
// the proxy ones. The proxy's own handlers check the same hosts in the other direction
// (config.UpsertProxy). The shape is NormalizeDomain's conflict: an invalid() with the
// host and its owner.
func siteHostClash(cfg *config.Config, s config.Site) error {
	if host, owner := cfg.SiteHostCollision(s); host != "" {
		return invalid("%s is already served by %s", host, owner)
	}
	if host, _, moved := cfg.SiteSelfCollision(s); host != "" {
		return invalid("%s is claimed twice by %s: the alias %s that follows the site lands on the pinned alias %s — remove one first", host, s.Name, moved, host)
	}
	return nil
}

// addDomainSuffix builds an alias entry from a label and a suffix (ruling R7: the suffix
// parameter is only a suffix, so domain is label-only and mutually exclusive with the
// full-host input). suffix "inherit" marks the alias inherited — it follows the site's
// effective suffix; any other suffix is normalized and validated (a public or refused one
// is a 400) and pins the alias: the entry carries its own suffix, and no global or site
// suffix change ever moves it (the Q2 rule). The resolved host goes through NormalizeDomain's
// checks — no site, proxy, or extra domain of the site may serve it.
func addDomainSuffix(cfg *config.Config, s *config.Site, domain, suffix string) (config.Domain, error) {
	label := strings.ToLower(strings.TrimSpace(domain))
	if !config.HostLabelOK(label) { // the one label rule (ruling R7: with suffix, domain is label-only)
		return config.Domain{}, invalid("%q is not a domain label: with a suffix the domain is a single label of a-z, 0-9 and inner hyphens", domain)
	}
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	tld := ""
	if suffix != "inherit" {
		tld = config.NormalizeTLD(suffix)
		if _, err := config.ValidateTLD(tld); err != nil {
			return config.Domain{}, invalid("%v", err)
		}
	}
	target := label + "." + s.Suffix(cfg) // inherit: the site's effective suffix
	if tld != "" {
		target = label + "." + tld
	}
	entry, err := cfg.NormalizeDomain(*s, target)
	if err != nil {
		return config.Domain{}, invalid("%v", err)
	}
	if !entry.Alias { // can't happen: a validated single-label suffix always reads as an alias
		return config.Domain{}, invalid("%s is not a suffix %s can use", suffix, s.Name)
	}
	entry.TLD = tld // force the marker: "inherit" → inherited, a suffix → pinned
	return entry, nil
}

func registerConfig(mux *http.ServeMux, b Backend) {
	// mutate runs change through the backend and answers with the new status.
	mutate := func(w http.ResponseWriter, change func(*config.Config) error) {
		if err := b.Mutate(change); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	}

	mux.HandleFunc("GET /v1/config", func(w http.ResponseWriter, _ *http.Request) {
		cfg, err := config.Load(b.Root())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, configView(b.Root(), cfg))
	})

	// TLD validation for the apps' settings, run while the user types. It only reports:
	// normalize, validate, answer; there is no b.Mutate here and every answer is HTTP 200
	// (an invalid TLD is a fact to show, not a bad request).
	mux.HandleFunc("POST /v1/tld/check", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			TLD string `json:"tld"`
		}
		out := TLDCheck{}
		if err := decodeBody(r, &body); err != nil {
			out.Error = err.Error()
		} else {
			out.Normalized = config.NormalizeTLD(body.TLD)
			warning, err := config.ValidateTLD(out.Normalized)
			out.OK = err == nil
			out.Warning = warning
			if err != nil {
				out.Error = err.Error()
			}
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("POST /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path    string `json:"path"`
			Name    string `json:"name"`
			PHP     string `json:"php"`
			Secured bool   `json:"secured"`
			// TLD is the site's own suffix ("loc" → served at <name>.loc); "" (like an
			// absent key) inherits the global one.
			TLD string `json:"tld"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			if !filepath.IsAbs(body.Path) {
				return invalid("path must be absolute")
			}
			if info, err := os.Stat(body.Path); err != nil || !info.IsDir() {
				return invalid("%s is not a directory", body.Path)
			}
			tld, err := siteTLD(body.TLD)
			if err != nil {
				return err
			}
			name := body.Name
			if name == "" {
				name = filepath.Base(body.Path)
			}
			site := config.Site{Name: config.SiteName(name), Path: filepath.Clean(body.Path), PHP: body.PHP, Secured: body.Secured, TLD: tld}
			// Create-time parity with PATCH (Goal 4): a suffix picked at create lands
			// the site's domain on a host Flock may already serve — the same landing
			// check the patch runs, refused with the config untouched (the closure).
			if err := siteHostClash(cfg, site); err != nil {
				return err
			}
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	mux.HandleFunc("PATCH /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Secured *bool   `json:"secured"`
			PHP     *string `json:"php"`
			Node    *string `json:"node"`
			// Server is "fastcgi" or "frankenphp"; Worker "" (classic) or "octane" (FrankenPHP's worker mode).
			Server *string `json:"server"`
			Worker *string `json:"worker"`
			// Root is the document root on this machine, relative to the project ("" lets the
			// project's .flockrc or detection decide again).
			Root *string `json:"root"`
			// TLD is the site's own suffix (ruling R7): absent or null keeps what it is,
			// "" is inherit, a value is normalized and validated (400 on reject, with the
			// config untouched). Raw JSON decides between absent and null.
			TLD json.RawMessage `json:"tld"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			if body.Secured != nil {
				site.Secured = *body.Secured
			}
			if len(body.TLD) > 0 && string(body.TLD) != "null" {
				var raw string
				if err := json.Unmarshal(body.TLD, &raw); err != nil {
					return invalid("tld must be a string (a suffix like loc, or \"\" for inherit), not %s", body.TLD)
				}
				tld, err := siteTLD(raw)
				if err != nil {
					return err
				}
				site.TLD = tld
				// The new suffix moves the site's domain and its inherited aliases:
				// none may land on a host Flock already serves — another site's
				// domain, any site's alias, a proxy's host (the CLI's siteHostsFree
				// runs the same config-side check on the same proposal).
				if err := siteHostClash(cfg, site); err != nil {
					return err
				}
			}
			if body.PHP != nil {
				installed, _ := php.Installed(b.Root())
				if *body.PHP != "" && !slices.Contains(installed, *body.PHP) {
					return invalid("PHP %s is not installed", *body.PHP)
				}
				site.PHP = *body.PHP
			}
			if body.Node != nil {
				if *body.Node != "" {
					if err := project.ValidVersion(project.Node, *body.Node); err != nil {
						return invalid("%v", err)
					}
				}
				site.Node = *body.Node
			}
			if body.Root != nil {
				site.Root = ""
				if *body.Root != "" {
					root, err := project.ValidRoot(site.Path, *body.Root)
					if err != nil {
						return invalid("%v", err)
					}
					site.Root = root
				}
			}
			if body.Server != nil || body.Worker != nil {
				server, worker := site.Server, site.Worker
				if body.Server != nil {
					server = *body.Server
				}
				if body.Worker != nil {
					worker = *body.Worker
				}
				if err := setServer(b, cfg, &site, server, worker); err != nil {
					return err
				}
			}
			// A parked site becomes linked so its settings can be stored.
			site.Parked = false
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	// Extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test"), see config.NormalizeDomain.
	// With suffix (ruling R7) the domain is a single label and the suffix decides the alias:
	// "inherit" marks it inherited, "loc" (normalized+validated) pins it to a suffix of its own.
	mux.HandleFunc("POST /v1/sites/{name}/domains", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
			Suffix string `json:"suffix"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			if body.Suffix == "" {
				if _, err := cfg.AddDomain(&site, body.Domain); err != nil {
					return invalid("%v", err)
				}
			} else {
				entry, err := addDomainSuffix(cfg, &site, body.Domain, body.Suffix)
				if err != nil {
					return err
				}
				site.Domains = append(site.Domains, entry)
			}
			site.Parked = false // a parked site becomes linked so the domain can be stored
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}/domains/{domain}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			if !cfg.RemoveDomain(&site, r.PathValue("domain")) {
				return invalid("%s is not an extra domain of %s", r.PathValue("domain"), site.Name)
			}
			if err := cfg.Upsert(site); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	// Pins a version into the project's files (by default the version the site uses now) and
	// lifts the site's isolation for that tool, so the project file decides from now on. With
	// tool "root", version is the document root (by default the one the site uses now).
	mux.HandleFunc("POST /v1/sites/{name}/pin", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tool    string `json:"tool"`
			Version string `json:"version"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			return pinSite(cfg, site, body.Tool, body.Version)
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}/pin/{tool}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			site, ok := cfg.FindSite(r.PathValue("name"))
			if !ok {
				return invalid("no site named %s", r.PathValue("name"))
			}
			tool := r.PathValue("tool")
			if tool != project.PHP && tool != project.Node && tool != project.Root {
				return invalid("unknown tool %q (php, node or root)", tool)
			}
			changed, err := project.Unpin(site.Path, tool)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				if tool == project.Root {
					return invalid("%s has no document root pinned in %s", site.Name, site.Path)
				}
				return invalid("%s has no %s version pinned in %s", site.Name, tool, site.Path)
			}
			return nil
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			if site, ok := cfg.FindSite(r.PathValue("name")); ok && site.IsWorktree() {
				return fmt.Errorf("%w: %s is a git worktree of %s; it stops being served when the worktree is removed", errConflict, site.Name, site.Parent)
			}
			if !cfg.Remove(r.PathValue("name")) {
				return fmt.Errorf("%w: %s is served from a parked directory; remove its parent from the project folders instead", errConflict, r.PathValue("name"))
			}
			return nil
		})
	})

	// A proxy passes its name to a server of its own (see config.Proxy). Secured is kept when left out.
	mux.HandleFunc("POST /v1/proxies", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name     string `json:"name"`
			Upstream string `json:"upstream"`
			Secured  *bool  `json:"secured"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			upstream, err := config.NormalizeUpstream(body.Upstream)
			if err != nil {
				return invalid("%v", err)
			}
			p := config.Proxy{Name: config.SiteName(body.Name), Upstream: upstream}
			if existing, ok := cfg.FindProxy(p.Name); ok {
				p.Secured = existing.Secured
			}
			if body.Secured != nil {
				p.Secured = *body.Secured
			}
			if err := cfg.UpsertProxy(p); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	mux.HandleFunc("PATCH /v1/proxies/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Secured *bool `json:"secured"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			p, ok := cfg.FindProxy(r.PathValue("name"))
			if !ok {
				return invalid("no proxy named %q", r.PathValue("name"))
			}
			if body.Secured != nil {
				p.Secured = *body.Secured
			}
			if err := cfg.UpsertProxy(p); err != nil {
				return invalid("%v", err)
			}
			return nil
		})
	})

	mux.HandleFunc("DELETE /v1/proxies/{name}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			if !cfg.RemoveProxy(r.PathValue("name")) {
				return invalid("no proxy named %q", r.PathValue("name"))
			}
			return nil
		})
	})

	parked := func(w http.ResponseWriter, r *http.Request, add bool) {
		var body struct {
			Path string `json:"path"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		mutate(w, func(cfg *config.Config) error {
			path := filepath.Clean(body.Path)
			if !filepath.IsAbs(path) {
				return invalid("path must be absolute")
			}
			i := slices.Index(cfg.Parked, path)
			switch {
			case add && i < 0:
				if info, err := os.Stat(path); err != nil || !info.IsDir() {
					return invalid("%s is not a directory", path)
				}
				cfg.Parked = append(cfg.Parked, path)
			case !add && i >= 0:
				cfg.Parked = slices.Delete(cfg.Parked, i, i+1)
			case !add:
				return invalid("%s is not parked", path)
			}
			return nil
		})
	}
	mux.HandleFunc("POST /v1/parked", func(w http.ResponseWriter, r *http.Request) { parked(w, r, true) })
	mux.HandleFunc("DELETE /v1/parked", func(w http.ResponseWriter, r *http.Request) { parked(w, r, false) })

	mux.HandleFunc("POST /v1/node/install", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Version string `json:"version"`
		}
		if err := decodeBody(r, &body); err != nil || body.Version == "" {
			writeError(w, http.StatusBadRequest, invalid("version is required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
		defer cancel()
		version, err := (&node.Installer{Root: b.Root(), Catalog: b.Binaries()}).Install(ctx, body.Version)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"version": version})
	})

	mux.HandleFunc("GET /v1/services", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Services())
	})

	service := func(w http.ResponseWriter, r *http.Request, change func(cfg *config.Config, id string) error) {
		if err := b.Mutate(func(cfg *config.Config) error { return change(cfg, r.PathValue("id")) }); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Services())
	}
	setRunning := func(on bool) func(*config.Config, string) error {
		return func(cfg *config.Config, id string) error {
			switch id {
			case "postgres":
				cfg.Services.SetRunning(id, on)
			case "dumps":
				cfg.Dumps = on
			default:
				if _, ok := cfg.Services.Extra(id); !ok {
					return invalid("%s cannot be started or stopped", id)
				}
				cfg.Services.SetRunning(id, on)
			}
			return nil
		}
	}
	mux.HandleFunc("POST /v1/services/{id}/start", func(w http.ResponseWriter, r *http.Request) { service(w, r, setRunning(true)) })
	mux.HandleFunc("POST /v1/services/{id}/stop", func(w http.ResponseWriter, r *http.Request) { service(w, r, setRunning(false)) })

	mux.HandleFunc("PATCH /v1/services/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Port int `json:"port"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		service(w, r, func(cfg *config.Config, id string) error {
			if body.Port < 1024 || body.Port > 65535 {
				return invalid("port must be between 1024 and 65535")
			}
			if other, taken := cfg.UsedPorts(id)[body.Port]; taken && other != id {
				return invalid("port %d is used by Flock's %s service", body.Port, other)
			}
			if _, current := cfg.UsedPorts("")[body.Port]; !current {
				if taken := ports.Taken(body.Port, b.Root()); taken != "" {
					return invalid("%s", taken)
				}
			}
			if x, ok := cfg.Services.Extra(id); ok {
				x.Port = body.Port
				cfg.Services.SetExtra(id, x)
				return nil
			}
			switch id {
			case "postgres":
				cfg.Ports.Postgres = body.Port
			case "mail":
				cfg.Ports.SMTP = body.Port
			case "dumps":
				cfg.Ports.Dumps = body.Port
			default:
				return invalid("the %s port can only be changed with `flock install`", id)
			}
			return nil
		})
	})
}

// SiteChange is a PATCH /v1/sites/{name} body; nil fields stay as they are.
type SiteChange struct {
	Secured *bool   `json:"secured,omitempty"`
	PHP     *string `json:"php,omitempty"`
	Node    *string `json:"node,omitempty"`
	Server  *string `json:"server,omitempty"`
	Worker  *string `json:"worker,omitempty"`
	Root    *string `json:"root,omitempty"`
	// TLD is the site's own suffix (ruling R7): nil leaves it alone, "" is inherit, a
	// value is normalized and validated (400 on reject, the config untouched).
	TLD *string `json:"tld,omitempty"`
}

// Config fetches config.json plus the installed versions.
func (c *Client) Config(ctx context.Context) (ConfigView, error) {
	var out ConfigView
	err := c.get(ctx, "/v1/config", &out)
	return out, err
}

// Services fetches every background service with its live state.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var out []Service
	err := c.get(ctx, "/v1/services", &out)
	return out, err
}

// SetServiceRunning starts or stops a service.
func (c *Client) SetServiceRunning(ctx context.Context, id string, on bool) ([]Service, error) {
	action := "stop"
	if on {
		action = "start"
	}
	var out []Service
	err := c.do(ctx, http.MethodPost, "/v1/services/"+url.PathEscape(id)+"/"+action, &out)
	return out, err
}

// LinkSite serves dir (absolute) as a site; name defaults to the directory's name.
func (c *Client) LinkSite(ctx context.Context, dir, name string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPost, "/v1/sites", map[string]string{"path": dir, "name": name}, &out)
	return out, err
}

// Proxy serves <name>.<tld> from upstream (a port, host:port or URL; see config.NormalizeUpstream),
// adding the proxy or changing its upstream. A nil secured keeps the proxy's setting (HTTP for a new one).
func (c *Client) Proxy(ctx context.Context, name, upstream string, secured *bool) (Status, error) {
	var out Status
	body := map[string]any{"name": name, "upstream": upstream}
	if secured != nil {
		body["secured"] = *secured
	}
	err := c.send(ctx, http.MethodPost, "/v1/proxies", body, &out)
	return out, err
}

// SecureProxy serves a proxy over HTTPS (on) or plain HTTP.
func (c *Client) SecureProxy(ctx context.Context, name string, on bool) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPatch, "/v1/proxies/"+url.PathEscape(name), map[string]bool{"secured": on}, &out)
	return out, err
}

// RemoveProxy stops serving a proxy.
func (c *Client) RemoveProxy(ctx context.Context, name string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodDelete, "/v1/proxies/"+url.PathEscape(name), nil, &out)
	return out, err
}

// UpdateSite changes a site's settings. Switching servers can take a while (FrankenPHP starts).
func (c *Client) UpdateSite(ctx context.Context, name string, change SiteChange) (Status, error) {
	var out Status
	err := c.timeout(time.Minute).send(ctx, http.MethodPatch, "/v1/sites/"+url.PathEscape(name), change, &out)
	return out, err
}

// AddDomain adds an extra domain to a site: a subdomain ("tenant1") or an alias
// ("my-app-2.test"). With suffix (ruling R7) the domain is a single label and the suffix
// decides the alias: "inherit" marks it inherited, "loc" pins it to a suffix of its own.
// An empty suffix keeps the legacy full-host semantics.
func (c *Client) AddDomain(ctx context.Context, name, domain, suffix string) (Status, error) {
	var out Status
	body := map[string]string{"domain": domain}
	if suffix != "" {
		body["suffix"] = suffix
	}
	err := c.send(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/domains", body, &out)
	return out, err
}

// PinSite pins a PHP or Node.js version into the site's project files (empty: the one it uses now).
func (c *Client) PinSite(ctx context.Context, name, tool, version string) (Status, error) {
	var out Status
	err := c.send(ctx, http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/pin", map[string]string{"tool": tool, "version": version}, &out)
	return out, err
}

// UnpinSite removes a pinned version from the site's project files.
func (c *Client) UnpinSite(ctx context.Context, name, tool string) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodDelete, "/v1/sites/"+url.PathEscape(name)+"/pin/"+url.PathEscape(tool), &out)
	return out, err
}

func configView(root string, cfg *config.Config) ConfigView {
	view := ConfigView{
		TLD: cfg.TLD, DefaultPHP: cfg.DefaultPHP, DefaultNode: cfg.DefaultNode,
		Xdebug: cfg.Xdebug, XdebugBreakpoints: cfg.XdebugBreakpoints, Dumps: cfg.Dumps, Channel: ChannelOf(cfg), Root: root, Ports: cfg.Ports,
		CaptureOff: append([]string{}, cfg.CaptureOff...), DumpsInPage: cfg.DumpsInPage,
		Parked: cfg.Parked, PHP: []PHPVersion{}, Node: []string{},
	}
	if view.Parked == nil {
		view.Parked = []string{}
	}

	installed, _ := php.Installed(root)
	for _, v := range installed {

		view.PHP = append(view.PHP, PHPVersion{
			Version:         v,
			FullVersion:     php.FullVersion(root, v),
			Ini:             php.IniPath(root, v),
			XdebugAvailable: php.XdebugAvailable(root, v),
		})
	}

	if versions, err := node.Installed(root); err == nil && versions != nil {
		view.Node = versions
	}
	return view
}

// ChannelOf is the channel config.json follows ("stable" when it names none or an unknown one).
func ChannelOf(cfg *config.Config) string {
	ch, err := binaries.ParseChannel(cfg.Channel)
	if err != nil {
		return string(binaries.Stable)
	}
	return string(ch)
}
