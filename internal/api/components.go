package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"flock/internal/components"
)

// Components is the state of Redis and the PostgreSQL client tools.
type Components = components.Components

func registerComponents(mux *http.ServeMux, b Backend) {
	manager := func(w http.ResponseWriter) *components.Manager {
		m := b.Components()
		if m == nil {
			writeError(w, http.StatusNotImplemented, errors.New("components are not supported on this platform"))
		}
		return m
	}
	respond := func(w http.ResponseWriter, m *components.Manager, err error) {
		if err != nil {
			writeError(w, componentStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, m.Components())
	}
	action := func(do func(m *components.Manager, id string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if m := manager(w); m != nil {
				respond(w, m, do(m, r.PathValue("id")))
			}
		}
	}

	mux.HandleFunc("GET /v1/components", func(w http.ResponseWriter, _ *http.Request) {
		if m := manager(w); m != nil {
			respond(w, m, nil)
		}
	})
	mux.HandleFunc("POST /v1/components/check", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		_ = m.Check(ctx) // a failed check is reported in check_error
		respond(w, m, nil)
	})
	mux.HandleFunc("POST /v1/components/{id}/install", action((*components.Manager).Start))
	mux.HandleFunc("POST /v1/components/{id}/cancel", action((*components.Manager).Cancel))
	mux.HandleFunc("DELETE /v1/components/{id}/job", action((*components.Manager).Dismiss))
	mux.HandleFunc("DELETE /v1/components/{id}", action((*components.Manager).Uninstall))

	mux.HandleFunc("GET /v1/components/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		if _, ok := m.Components().Find(r.PathValue("id")); !ok {
			writeError(w, http.StatusNotFound, errors.New("unknown component"))
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

func componentStatus(err error) int {
	switch {
	case errors.Is(err, components.ErrUnknown):
		return http.StatusNotFound
	case errors.Is(err, components.ErrBusy), errors.Is(err, components.ErrUpToDate):
		return http.StatusConflict
	}
	return statusFor(err)
}

// Components fetches the state of Redis and the PostgreSQL client tools.
func (c *Client) Components(ctx context.Context) (Components, error) {
	var out Components
	err := c.get(ctx, "/v1/components", &out)
	return out, err
}
