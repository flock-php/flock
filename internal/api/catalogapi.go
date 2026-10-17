package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"flock/internal/catalog"
)

// Catalog is the state of the services Flock installs on request (MySQL, Redis, Meilisearch, ...).
type Catalog = catalog.Catalog

func registerCatalog(mux *http.ServeMux, b Backend) {
	manager := func(w http.ResponseWriter) *catalog.Manager {
		m := b.Catalog()
		if m == nil {
			writeError(w, http.StatusNotImplemented, errors.New("the service catalog is not supported on this platform"))
		}
		return m
	}
	respond := func(w http.ResponseWriter, m *catalog.Manager, err error) {
		if err != nil {
			writeError(w, catalogStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, m.Catalog())
	}
	action := func(do func(m *catalog.Manager, r *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if m := manager(w); m != nil {
				respond(w, m, do(m, r))
			}
		}
	}

	mux.HandleFunc("GET /v1/catalog", action(func(*catalog.Manager, *http.Request) error { return nil }))
	mux.HandleFunc("POST /v1/catalog/check", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		_ = m.Check(ctx) // a failed check is reported in check_error
		respond(w, m, nil)
	})
	// Body (optional): {"line": "8.4", "port": 3307}.
	mux.HandleFunc("POST /v1/catalog/{id}/install", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		var body struct {
			Line string `json:"line"`
			Port int    `json:"port"`
		}
		if r.ContentLength != 0 {
			if err := decodeBody(r, &body); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		respond(w, m, m.Start(r.PathValue("id"), body.Line, body.Port))
	})
	// Body: {"line": "8.4"}, an installed line to run instead of the active one.
	mux.HandleFunc("POST /v1/catalog/{id}/activate", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		var body struct {
			Line string `json:"line"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		respond(w, m, m.Activate(r.PathValue("id"), body.Line))
	})
	mux.HandleFunc("POST /v1/catalog/{id}/cancel", action(func(m *catalog.Manager, r *http.Request) error { return m.Cancel(r.PathValue("id")) }))
	mux.HandleFunc("DELETE /v1/catalog/{id}/job", action(func(m *catalog.Manager, r *http.Request) error { return m.Dismiss(r.PathValue("id")) }))
	// ?line=8.4 removes one line (else every line); ?delete_data=1 also deletes its data.
	mux.HandleFunc("DELETE /v1/catalog/{id}", action(func(m *catalog.Manager, r *http.Request) error {
		deleteData, _ := strconv.ParseBool(r.URL.Query().Get("delete_data"))
		return m.Uninstall(r.PathValue("id"), r.URL.Query().Get("line"), deleteData)
	}))

	mux.HandleFunc("GET /v1/catalog/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		if _, err := m.Kind(r.PathValue("id")); err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		text, err := tailLines(m.LogPath(r.PathValue("id")), 200)
		if err != nil {
			writeError(w, http.StatusNotFound, errors.New("no install log yet"))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(text))
	})
}

func catalogStatus(err error) int {
	switch {
	case errors.Is(err, catalog.ErrUnknown):
		return http.StatusNotFound
	case errors.Is(err, catalog.ErrInvalid), errors.Is(err, catalog.ErrUnavailable):
		return http.StatusBadRequest
	case errors.Is(err, catalog.ErrBusy):
		return http.StatusConflict
	}
	return statusFor(err)
}

// Catalog fetches the state of the catalog services.
func (c *Client) Catalog(ctx context.Context) (Catalog, error) {
	var out Catalog
	err := c.get(ctx, "/v1/catalog", &out)
	return out, err
}

// InstallService starts installing a catalog service (line and port may be empty/0 for the defaults).
func (c *Client) InstallService(ctx context.Context, id, line string, port int) (Catalog, error) {
	var out Catalog
	err := c.send(ctx, http.MethodPost, "/v1/catalog/"+url.PathEscape(id)+"/install", map[string]any{"line": line, "port": port}, &out)
	return out, err
}

// ActivateService runs an installed line of a catalog service instead of the active one.
func (c *Client) ActivateService(ctx context.Context, id, line string) (Catalog, error) {
	var out Catalog
	err := c.slow().send(ctx, http.MethodPost, "/v1/catalog/"+url.PathEscape(id)+"/activate", map[string]any{"line": line}, &out)
	return out, err
}

// UninstallService removes a line of a catalog service ("" for every line), and its data when
// deleteData is set.
func (c *Client) UninstallService(ctx context.Context, id, line string, deleteData bool) (Catalog, error) {
	query := url.Values{}
	if line != "" {
		query.Set("line", line)
	}
	if deleteData {
		query.Set("delete_data", "1")
	}
	path := "/v1/catalog/" + url.PathEscape(id)
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var out Catalog
	err := c.slow().do(ctx, http.MethodDelete, path, &out)
	return out, err
}

// CancelService stops a running install of a catalog service.
func (c *Client) CancelService(ctx context.Context, id string) (Catalog, error) {
	var out Catalog
	err := c.do(ctx, http.MethodPost, "/v1/catalog/"+url.PathEscape(id)+"/cancel", &out)
	return out, err
}
