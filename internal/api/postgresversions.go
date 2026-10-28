package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"flock/internal/postgres"
)

// PostgresVersions is the PostgreSQL pane's data: every offered major with its install state.
type PostgresVersions = postgres.Versions

// PostgresExtensions is the extensions sheet's data: every extension for one major.
type PostgresExtensions = postgres.MajorExtensions

// PostgresInstall is the optional body of POST /v1/postgres/versions/{major}/install.
type PostgresInstall struct {
	Extensions []string `json:"extensions"` // installed for the major too, where it has a build
}

func registerPostgresVersions(mux *http.ServeMux, b Backend) {
	manager := func(w http.ResponseWriter) *postgres.Manager {
		m := b.PostgresVersions()
		if m == nil {
			writeError(w, http.StatusNotImplemented, errors.New("PostgreSQL is not supported on this platform"))
		}
		return m
	}
	respond := func(w http.ResponseWriter, m *postgres.Manager, err error) {
		if err != nil {
			writeError(w, postgresStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, m.Versions())
	}

	mux.HandleFunc("GET /v1/postgres/versions", func(w http.ResponseWriter, _ *http.Request) {
		if m := manager(w); m != nil {
			respond(w, m, nil)
		}
	})

	mux.HandleFunc("POST /v1/postgres/check", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		_ = m.Check(ctx) // a failed check is reported in check_error
		respond(w, m, nil)
	})

	// The operations outlive a client that gives up waiting: they change the running server, so
	// stopping halfway would leave it in between.
	operation := func(timeout time.Duration, do func(ctx context.Context, m *postgres.Manager, r *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m := manager(w)
			if m == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
			defer cancel()
			respond(w, m, do(ctx, m, r))
		}
	}

	// Installs the newest build of a major (or updates to it), with the extensions in the body.
	mux.HandleFunc("POST /v1/postgres/versions/{major}/install", operation(10*time.Minute, func(ctx context.Context, m *postgres.Manager, r *http.Request) error {
		var body PostgresInstall
		if r.ContentLength != 0 {
			if err := decodeBody(r, &body); err != nil {
				return err
			}
		}
		return m.Install(ctx, r.PathValue("major"), body.Extensions...)
	}))
	mux.HandleFunc("POST /v1/postgres/versions/{major}/update", operation(10*time.Minute, func(ctx context.Context, m *postgres.Manager, r *http.Request) error {
		return m.Update(ctx, r.PathValue("major"))
	}))
	mux.HandleFunc("POST /v1/postgres/versions/{major}/activate", operation(2*time.Minute, func(ctx context.Context, m *postgres.Manager, r *http.Request) error {
		return m.Activate(ctx, r.PathValue("major"))
	}))
	mux.HandleFunc("DELETE /v1/postgres/versions/{major}", operation(2*time.Minute, func(ctx context.Context, m *postgres.Manager, r *http.Request) error {
		deleteData := r.URL.Query().Get("delete_data")
		return m.Uninstall(ctx, r.PathValue("major"), deleteData == "1" || deleteData == "true")
	}))

	// The extensions of one major. The custom folder is created on the first look, so the apps
	// can open it.
	extensions := func(w http.ResponseWriter, m *postgres.Manager, major string, err error) {
		if err == nil {
			_, err = postgres.EnsureCustomDir(m.Installer.Root, major)
		}
		var state PostgresExtensions
		if err == nil {
			state, err = m.Extensions(major)
		}
		if err != nil {
			writeError(w, postgresStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	}
	extensionOperation := func(do func(ctx context.Context, m *postgres.Manager, major, name string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m := manager(w)
			if m == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
			defer cancel()
			major := r.PathValue("major")
			extensions(w, m, major, do(ctx, m, major, r.PathValue("name")))
		}
	}
	mux.HandleFunc("GET /v1/postgres/versions/{major}/extensions", func(w http.ResponseWriter, r *http.Request) {
		if m := manager(w); m != nil {
			extensions(w, m, r.PathValue("major"), nil)
		}
	})
	mux.HandleFunc("POST /v1/postgres/versions/{major}/extensions/{name}/install", extensionOperation(func(ctx context.Context, m *postgres.Manager, major, name string) error {
		return m.InstallExtension(ctx, major, name)
	}))
	mux.HandleFunc("POST /v1/postgres/versions/{major}/extensions/{name}/update", extensionOperation(func(ctx context.Context, m *postgres.Manager, major, name string) error {
		return m.UpdateExtension(ctx, major, name)
	}))
	mux.HandleFunc("DELETE /v1/postgres/versions/{major}/extensions/{name}", extensionOperation(func(ctx context.Context, m *postgres.Manager, major, name string) error {
		return m.UninstallExtension(ctx, major, name)
	}))
}

func postgresStatus(err error) int {
	switch {
	case errors.Is(err, postgres.ErrInvalidMajor), errors.Is(err, postgres.ErrUnknownExtension):
		return http.StatusBadRequest
	case errors.Is(err, postgres.ErrBusy), errors.Is(err, postgres.ErrNotInstalled), errors.Is(err, postgres.ErrPortTaken):
		return http.StatusConflict
	}
	return statusFor(err)
}

// PostgresVersions fetches every PostgreSQL major with its install state.
func (c *Client) PostgresVersions(ctx context.Context) (PostgresVersions, error) {
	var out PostgresVersions
	err := c.get(ctx, "/v1/postgres/versions", &out)
	return out, err
}

// CheckPostgres makes the daemon look up the newest PostgreSQL releases now.
func (c *Client) CheckPostgres(ctx context.Context) (PostgresVersions, error) {
	var out PostgresVersions
	err := c.timeout(time.Minute).do(ctx, http.MethodPost, "/v1/postgres/check", &out)
	return out, err
}

// InstallPostgres installs the newest build of a major, with extensions.
func (c *Client) InstallPostgres(ctx context.Context, major string, extensions ...string) (PostgresVersions, error) {
	var out PostgresVersions
	var body any
	if len(extensions) > 0 {
		body = PostgresInstall{Extensions: extensions}
	}
	err := c.timeout(10*time.Minute).send(ctx, http.MethodPost, "/v1/postgres/versions/"+url.PathEscape(major)+"/install", body, &out)
	return out, err
}

// UpdatePostgres updates an installed major to its newest release.
func (c *Client) UpdatePostgres(ctx context.Context, major string) (PostgresVersions, error) {
	var out PostgresVersions
	err := c.timeout(10*time.Minute).do(ctx, http.MethodPost, "/v1/postgres/versions/"+url.PathEscape(major)+"/update", &out)
	return out, err
}

// ActivatePostgres makes an installed major the one that runs.
func (c *Client) ActivatePostgres(ctx context.Context, major string) (PostgresVersions, error) {
	var out PostgresVersions
	err := c.timeout(2*time.Minute).do(ctx, http.MethodPost, "/v1/postgres/versions/"+url.PathEscape(major)+"/activate", &out)
	return out, err
}

// UninstallPostgres removes a major, and its databases when deleteData is set.
func (c *Client) UninstallPostgres(ctx context.Context, major string, deleteData bool) (PostgresVersions, error) {
	path := "/v1/postgres/versions/" + url.PathEscape(major)
	if deleteData {
		path += "?delete_data=1"
	}
	var out PostgresVersions
	err := c.timeout(2*time.Minute).do(ctx, http.MethodDelete, path, &out)
	return out, err
}

func extensionPath(major, name string) string {
	return "/v1/postgres/versions/" + url.PathEscape(major) + "/extensions/" + url.PathEscape(name)
}

// PostgresExtensions fetches the extensions of a major, creating its custom folder.
func (c *Client) PostgresExtensions(ctx context.Context, major string) (PostgresExtensions, error) {
	var out PostgresExtensions
	err := c.get(ctx, "/v1/postgres/versions/"+url.PathEscape(major)+"/extensions", &out)
	return out, err
}

// InstallPostgresExtension installs an extension for a major.
func (c *Client) InstallPostgresExtension(ctx context.Context, major, name string) (PostgresExtensions, error) {
	var out PostgresExtensions
	err := c.timeout(10*time.Minute).do(ctx, http.MethodPost, extensionPath(major, name)+"/install", &out)
	return out, err
}

// UpdatePostgresExtension updates an extension of a major to its newest build.
func (c *Client) UpdatePostgresExtension(ctx context.Context, major, name string) (PostgresExtensions, error) {
	var out PostgresExtensions
	err := c.timeout(10*time.Minute).do(ctx, http.MethodPost, extensionPath(major, name)+"/update", &out)
	return out, err
}

// UninstallPostgresExtension removes an extension from a major.
func (c *Client) UninstallPostgresExtension(ctx context.Context, major, name string) (PostgresExtensions, error) {
	var out PostgresExtensions
	err := c.timeout(10*time.Minute).do(ctx, http.MethodDelete, extensionPath(major, name), &out)
	return out, err
}
