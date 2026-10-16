package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"flock/internal/components"
	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/php"
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
	PHPAutoBuild      bool         `json:"php_auto_build"`
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

// pinSite pins a version into the site's project (see project.PinSite), storing the site when
// its isolation was lifted.
func pinSite(cfg *config.Config, site config.Site, tool, version string) error {
	if _, lifted, err := project.PinSite(cfg, &site, tool, version); err != nil {
		if errors.Is(err, project.ErrInvalid) {
			return invalid("%v", err)
		}
		return err
	} else if lifted {
		cfg.Upsert(site)
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
	if s.DefaultPHP != nil {
		installed, _ := php.Installed(root)
		if !slices.Contains(installed, *s.DefaultPHP) {
			return invalid("PHP %s is not installed", *s.DefaultPHP)
		}
		cfg.DefaultPHP = *s.DefaultPHP
	}
	if s.PHPAutoBuild != nil {
		cfg.PHPAutoBuild = *s.PHPAutoBuild
	}
	if s.DefaultNode != nil {
		if *s.DefaultNode == "" {
			return invalid("default_node must not be empty")
		}
		cfg.DefaultNode = *s.DefaultNode
	}
	return nil
}

func decodeBody(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v); err != nil {
		return invalid("%v", err)
	}
	return nil
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

	mux.HandleFunc("POST /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path    string `json:"path"`
			Name    string `json:"name"`
			PHP     string `json:"php"`
			Secured bool   `json:"secured"`
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
			name := body.Name
			if name == "" {
				name = filepath.Base(body.Path)
			}
			cfg.Upsert(config.Site{Name: config.SiteName(name), Path: filepath.Clean(body.Path), PHP: body.PHP, Secured: body.Secured})
			return nil
		})
	})

	mux.HandleFunc("PATCH /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Secured *bool   `json:"secured"`
			PHP     *string `json:"php"`
			Node    *string `json:"node"`
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
			// A parked site becomes linked so its settings can be stored.
			site.Parked = false
			cfg.Upsert(site)
			return nil
		})
	})

	// Extra domains: a subdomain ("tenant1") or an alias ("my-app-2.test"), see config.NormalizeDomain.
	mux.HandleFunc("POST /v1/sites/{name}/domains", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain string `json:"domain"`
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
			if _, err := cfg.AddDomain(&site, body.Domain); err != nil {
				return invalid("%v", err)
			}
			site.Parked = false // a parked site becomes linked so the domain can be stored
			cfg.Upsert(site)
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
			cfg.Upsert(site)
			return nil
		})
	})

	// Pins a version into the project's files (by default the version the site uses now) and
	// lifts the site's isolation for that tool, so the project file decides from now on.
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
			if tool != project.PHP && tool != project.Node {
				return invalid("unknown tool %q (php or node)", tool)
			}
			changed, err := project.Unpin(site.Path, tool)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				return invalid("%s has no %s version pinned in %s", site.Name, tool, site.Path)
			}
			return nil
		})
	})

	mux.HandleFunc("DELETE /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		mutate(w, func(cfg *config.Config) error {
			if !cfg.Remove(r.PathValue("name")) {
				return fmt.Errorf("%w: %s is served from a parked directory; remove its parent from the project folders instead", errConflict, r.PathValue("name"))
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
		version, err := (&node.Installer{Root: b.Root()}).Install(ctx, body.Version)
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
			case "postgres", components.Valkey:
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
			if x, ok := cfg.Services.Extra(id); ok {
				x.Port = body.Port
				cfg.Services.SetExtra(id, x)
				return nil
			}
			switch id {
			case "postgres":
				cfg.Ports.Postgres = body.Port
			case components.Valkey:
				cfg.Ports.Valkey = body.Port
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

func configView(root string, cfg *config.Config) ConfigView {
	view := ConfigView{
		TLD: cfg.TLD, DefaultPHP: cfg.DefaultPHP, DefaultNode: cfg.DefaultNode,
		Xdebug: cfg.Xdebug, XdebugBreakpoints: cfg.XdebugBreakpoints, Dumps: cfg.Dumps, PHPAutoBuild: cfg.PHPAutoBuild, Root: root, Ports: cfg.Ports,
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
