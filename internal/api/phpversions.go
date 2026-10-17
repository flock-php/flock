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
)

// PHPVersions is the PHP pane's data: every version the channel offers or that is installed.
type PHPVersions struct {
	CheckedAt  *time.Time   `json:"checked_at"`
	CheckError string       `json:"check_error"`
	Versions   []PHPRelease `json:"versions"`
}

// PHPRelease is a php.Release plus what only the daemon knows.
type PHPRelease struct {
	php.Release
	Default bool `json:"default"`
	Xdebug  bool `json:"xdebug"` // the installed build has Xdebug
}

func registerPHPVersions(mux *http.ServeMux, b Backend) {
	respond := func(w http.ResponseWriter, err error) {
		if err != nil {
			code := statusFor(err)
			if errors.Is(err, php.ErrBusy) {
				code = http.StatusConflict
			}
			writeError(w, code, err)
			return
		}
		writeJSON(w, http.StatusOK, phpVersions(b))
	}
	minor := func(r *http.Request) (string, error) {
		v := r.PathValue("minor")
		if php.MinorOf(v) != v {
			return "", invalid("invalid PHP version %q", v)
		}
		return v, nil
	}
	action := func(do func(m *php.Manager, minor string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			v, err := minor(r)
			if err == nil {
				err = do(b.PHP(), v)
			}
			respond(w, err)
		}
	}

	mux.HandleFunc("GET /v1/php/versions", func(w http.ResponseWriter, _ *http.Request) { respond(w, nil) })
	mux.HandleFunc("POST /v1/php/check", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		_ = b.PHP().Check(ctx) // a failed check is reported in check_error
		respond(w, nil)
	})
	// Installs the channel's newest release of a minor, updates to it, or retries a failed install.
	mux.HandleFunc("POST /v1/php/versions/{minor}/install", action(func(m *php.Manager, v string) error {
		m.Dismiss(v)
		return m.Install(v)
	}))
	mux.HandleFunc("POST /v1/php/versions/{minor}/cancel", action(func(m *php.Manager, v string) error { m.Cancel(v); return nil }))
	mux.HandleFunc("DELETE /v1/php/versions/{minor}/job", action(func(m *php.Manager, v string) error { m.Dismiss(v); return nil }))

	mux.HandleFunc("DELETE /v1/php/versions/{minor}", action(func(m *php.Manager, v string) error {
		if m.Busy(v) {
			return php.ErrBusy
		}
		if err := b.Mutate(func(cfg *config.Config) error { return checkUninstall(b.Root(), cfg, v) }); err != nil {
			return err
		}
		b.ReleasePHP(v)
		if err := php.Uninstall(b.Root(), v); err != nil {
			return err
		}
		return b.Reload()
	}))

	mux.HandleFunc("GET /v1/php/versions/{minor}/log", func(w http.ResponseWriter, r *http.Request) {
		v, err := minor(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		text, err := tailLines(php.LogPath(b.Root(), v), 2000)
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("no install log yet"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(text))
	})
}

// PHPVersions fetches every PHP version with its install state.
func (c *Client) PHPVersions(ctx context.Context) (PHPVersions, error) {
	var out PHPVersions
	err := c.get(ctx, "/v1/php/versions", &out)
	return out, err
}

// CheckPHP makes the daemon fetch the manifest now.
func (c *Client) CheckPHP(ctx context.Context) (PHPVersions, error) {
	var out PHPVersions
	err := c.slow().do(ctx, http.MethodPost, "/v1/php/check", &out)
	return out, err
}

// InstallPHP starts installing the newest release of minor (follow it with PHPVersions).
func (c *Client) InstallPHP(ctx context.Context, minor string) (PHPVersions, error) {
	var out PHPVersions
	err := c.do(ctx, http.MethodPost, "/v1/php/versions/"+minor+"/install", &out)
	return out, err
}

// CancelPHPInstall stops a running install of minor.
func (c *Client) CancelPHPInstall(ctx context.Context, minor string) error {
	var out PHPVersions
	return c.do(ctx, http.MethodPost, "/v1/php/versions/"+minor+"/cancel", &out)
}

// Find returns the version with this minor, if the daemon knows it.
func (v PHPVersions) Find(minor string) (PHPRelease, bool) {
	for _, r := range v.Versions {
		if r.Minor == minor {
			return r, true
		}
	}
	return PHPRelease{}, false
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
	m := b.PHP()
	cfg, _ := config.Load(b.Root())
	if cfg == nil {
		cfg = config.Default()
	}

	checkedAt, checkErr := m.CheckedAt()
	out := PHPVersions{CheckError: checkErr, Versions: []PHPRelease{}}
	if !checkedAt.IsZero() {
		out.CheckedAt = &checkedAt
	}

	for _, r := range m.Releases() {
		release := PHPRelease{Release: r, Default: r.Installed != "" && r.Minor == cfg.DefaultPHP}
		if r.Installed != "" {
			release.Xdebug = php.XdebugAvailable(b.Root(), r.Minor)
		}
		out.Versions = append(out.Versions, release)
	}
	return out
}
