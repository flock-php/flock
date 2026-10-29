package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"flock/internal/share"
)

// ShareStatus is one site's tunnel (`flock share`).
type ShareStatus = share.Status

// ShareRequest starts a share: Allow are the emails (or *@domain) let in, none for anyone with the URL.
// Debug turns the app's debug mode on for visitors, whatever .env says.
type ShareRequest struct {
	Allow []string `json:"allow"`
	Debug bool     `json:"debug,omitempty"`
}

func registerShares(mux *http.ServeMux, b Backend) {
	manager := func(w http.ResponseWriter) *share.Manager {
		m := b.Shares()
		if m == nil {
			writeError(w, http.StatusNotImplemented, errors.New("sharing isn't available"))
		}
		return m
	}

	mux.HandleFunc("GET /v1/shares", func(w http.ResponseWriter, _ *http.Request) {
		if m := manager(w); m != nil {
			writeJSON(w, http.StatusOK, m.List())
		}
	})
	mux.HandleFunc("GET /v1/shares/{site}", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		st := m.Get(r.PathValue("site"))
		if st == nil {
			writeError(w, http.StatusNotFound, fmt.Errorf("%s isn't shared", r.PathValue("site")))
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /v1/shares/{site}", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		site, ok := findSite(w, b, r.PathValue("site"))
		if !ok {
			return
		}
		var req ShareRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid share request: %w", err))
			return
		}
		if err := b.StartShare(site.Name, req.Allow, req.Debug); err != nil {
			code := http.StatusConflict
			if errors.Is(err, share.ErrInvalid) {
				code = http.StatusBadRequest
			}
			writeError(w, code, err)
			return
		}
		writeJSON(w, http.StatusOK, m.Get(site.Name))
	})
	mux.HandleFunc("POST /v1/shares/{site}/rebuild", func(w http.ResponseWriter, r *http.Request) {
		m := manager(w)
		if m == nil {
			return
		}
		if err := m.Rebuild(r.PathValue("site")); err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, m.Get(r.PathValue("site")))
	})
	mux.HandleFunc("DELETE /v1/shares/{site}", func(w http.ResponseWriter, r *http.Request) {
		if m := manager(w); m != nil {
			m.Stop(r.PathValue("site"))
			writeJSON(w, http.StatusOK, m.List())
		}
	})
}

// StartShare shares a site; Share follows it from building to running.
func (c *Client) StartShare(ctx context.Context, site string, allow []string, debug bool) (ShareStatus, error) {
	var st ShareStatus
	err := c.send(ctx, http.MethodPost, "/v1/shares/"+url.PathEscape(site), ShareRequest{Allow: allow, Debug: debug}, &st)
	return st, err
}

// Share is a site's share; an error when it has none.
func (c *Client) Share(ctx context.Context, site string) (ShareStatus, error) {
	var st ShareStatus
	err := c.get(ctx, "/v1/shares/"+url.PathEscape(site), &st)
	return st, err
}

// RebuildShare builds a running share's assets again.
func (c *Client) RebuildShare(ctx context.Context, site string) (ShareStatus, error) {
	var st ShareStatus
	err := c.do(ctx, http.MethodPost, "/v1/shares/"+url.PathEscape(site)+"/rebuild", &st)
	return st, err
}

// StopShare ends a site's share.
func (c *Client) StopShare(ctx context.Context, site string) error {
	var list []ShareStatus
	return c.send(ctx, http.MethodDelete, "/v1/shares/"+url.PathEscape(site), nil, &list)
}

// Shares lists every share.
func (c *Client) Shares(ctx context.Context) ([]ShareStatus, error) {
	var list []ShareStatus
	err := c.get(ctx, "/v1/shares", &list)
	return list, err
}
