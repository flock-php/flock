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

	// Installs the newest build of a major (or updates to it).
	mux.HandleFunc("POST /v1/postgres/versions/{major}/install", operation(10*time.Minute, func(ctx context.Context, m *postgres.Manager, r *http.Request) error {
		return m.Install(ctx, r.PathValue("major"))
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
}

func postgresStatus(err error) int {
	switch {
	case errors.Is(err, postgres.ErrInvalidMajor):
		return http.StatusBadRequest
	case errors.Is(err, postgres.ErrBusy), errors.Is(err, postgres.ErrNotInstalled):
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

// InstallPostgres installs the newest build of a major.
func (c *Client) InstallPostgres(ctx context.Context, major string) (PostgresVersions, error) {
	var out PostgresVersions
	err := c.timeout(10*time.Minute).do(ctx, http.MethodPost, "/v1/postgres/versions/"+url.PathEscape(major)+"/install", &out)
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
