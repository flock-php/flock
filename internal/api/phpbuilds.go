package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"flock/internal/config"
	"flock/internal/php"
	"flock/internal/phpbuild"
)

// PHPVersions is the PHP pane's data: every version with its install/build state.
type PHPVersions struct {
	AutoBuild  bool         `json:"auto_build"`
	CheckedAt  *time.Time   `json:"checked_at"`
	CheckError string       `json:"check_error"`
	Versions   []PHPRelease `json:"versions"`
}

// PHPRelease is a phpbuild.Version plus what only the daemon knows.
type PHPRelease struct {
	phpbuild.Version
	Default bool `json:"default"`
	Xdebug  bool `json:"xdebug"` // the installed build has xdebug.so
}

func registerPHPBuilds(mux *http.ServeMux, b Backend) {
	respond := func(w http.ResponseWriter, err error) {
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, phpVersions(b))
	}
	manager := func(w http.ResponseWriter) *phpbuild.Manager {
		m := b.PHPBuilds()
		if m == nil {
			writeError(w, http.StatusNotImplemented, errors.New("building PHP is not supported on this platform yet"))
		}
		return m
	}
	minor := func(r *http.Request) (string, error) {
		v := r.PathValue("minor")
		if phpbuild.MinorOf(v) != v {
			return "", invalid("invalid PHP version %q", v)
		}
		return v, nil
	}

	mux.HandleFunc("GET /v1/php/versions", func(w http.ResponseWriter, _ *http.Request) {
		if manager(w) != nil {
			respond(w, nil)
		}
	})

	mux.HandleFunc("POST /v1/php/check", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		_ = m.Check(ctx) // a failed check is reported in check_error
		respond(w, nil)
	})

	action := func(do func(m *phpbuild.Manager, minor string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m := manager(w)
			if m == nil {
				return
			}
			v, err := minor(r)
			if err == nil {
				err = do(m, v)
			}
			respond(w, err)
		}
	}
	mux.HandleFunc("POST /v1/php/versions/{minor}/install", action(func(m *phpbuild.Manager, v string) error { return m.Install(v) }))
	mux.HandleFunc("POST /v1/php/versions/{minor}/build", action(func(m *phpbuild.Manager, v string) error { m.Build(v); return nil }))
	mux.HandleFunc("POST /v1/php/versions/{minor}/cancel", action(func(m *phpbuild.Manager, v string) error { m.Cancel(v); return nil }))

	mux.HandleFunc("DELETE /v1/php/versions/{minor}", func(w http.ResponseWriter, r *http.Request) {
		v, err := minor(r)
		if err == nil {
			err = b.Mutate(func(cfg *config.Config) error { return checkUninstall(b.Root(), cfg, v) })
		}
		if err == nil {
			b.ReleasePHP(v)
			err = php.Uninstall(b.Root(), v)
			if err == nil {
				err = b.Reload()
			}
		}
		if b.PHPBuilds() == nil {
			if err != nil {
				writeError(w, statusFor(err), err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		respond(w, err)
	})

	mux.HandleFunc("GET /v1/php/versions/{minor}/log", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		v, err := minor(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		text, err := tailLines(m.LogPath(v), 2000)
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("no build log yet"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(text))
	})
}

// checkUninstall refuses to remove the default version or one a linked site is pinned to.
func checkUninstall(root string, cfg *config.Config, minor string) error {
	installed, _ := php.Installed(root)
	if !slices.Contains(installed, minor) {
		return invalid("PHP %s is not installed", minor)
	}
	if cfg.DefaultPHP == minor {
		return fmt.Errorf("%w: PHP %s is the default version — make another version the default first", errConflict, minor)
	}
	for _, s := range cfg.Sites {
		if s.PHP == minor {
			return fmt.Errorf("%w: the site %s uses PHP %s", errConflict, s.Name, minor)
		}
	}
	return nil
}

func phpVersions(b Backend) PHPVersions {
	m := b.PHPBuilds()
	cfg, _ := config.Load(b.Root())
	if cfg == nil {
		cfg = config.Default()
	}

	checkedAt, checkErr := m.CheckedAt()
	out := PHPVersions{AutoBuild: cfg.PHPAutoBuild, CheckError: checkErr, Versions: []PHPRelease{}}
	if !checkedAt.IsZero() {
		out.CheckedAt = &checkedAt
	}

	for _, v := range m.Versions() {
		release := PHPRelease{Version: v, Default: v.Installed != "" && v.Minor == cfg.DefaultPHP}
		if v.Installed != "" {
			release.Xdebug = fileExists(php.ExtensionPath(b.Root(), v.Minor, "xdebug"))
		}
		out.Versions = append(out.Versions, release)
	}
	return out
}
