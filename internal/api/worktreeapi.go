package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"flock/internal/worktree"
)

// WorktreeStatus is the setup of a git worktree Flock serves (see package worktree).
type WorktreeStatus = worktree.Status

// ErrNotWorktree is returned for a path that isn't a worktree of a Flock site.
var ErrNotWorktree = errors.New("not a git worktree of a Flock site")

// WorktreeRequest names a worktree by its folder. Again sets it up again although it is set up
// already; Force removes it even when a hook fails or it has changes.
type WorktreeRequest struct {
	Path  string `json:"path"`
	Again bool   `json:"again,omitempty"`
	Force bool   `json:"force,omitempty"`
}

// WorktreeRemoval is what removing a worktree printed (its before_delete hook, git).
type WorktreeRemoval struct {
	Output string `json:"output"`
}

func registerWorktrees(mux *http.ServeMux, b Backend) {
	decode := func(w http.ResponseWriter, r *http.Request) (WorktreeRequest, bool) {
		var req WorktreeRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Path == "" {
			writeError(w, http.StatusBadRequest, errors.New("name the worktree by its path"))
			return req, false
		}
		return req, true
	}
	fail := func(w http.ResponseWriter, err error) {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrNotWorktree) {
			code = http.StatusNotFound
		}
		writeError(w, code, err)
	}

	// Starts the setup, or joins the one running; see Backend.SetupWorktree.
	mux.HandleFunc("POST /v1/worktrees/setup", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		st, err := b.SetupWorktree(req.Path, req.Again)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("GET /v1/worktrees/setup", func(w http.ResponseWriter, r *http.Request) {
		st, err := b.WorktreeSetup(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		if st == nil {
			writeError(w, http.StatusNotFound, errors.New("Flock hasn't set this worktree up"))
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /v1/worktrees/remove", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		out, err := b.RemoveWorktree(r.Context(), req.Path, req.Force)
		if err != nil {
			if out != "" {
				err = fmt.Errorf("%w\n%s", err, out)
			}
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, WorktreeRemoval{Output: out})
	})
}

// SetupWorktree starts the setup of the worktree at path, or joins the one that runs.
func (c *Client) SetupWorktree(ctx context.Context, path string, again bool) (WorktreeStatus, error) {
	var st WorktreeStatus
	err := c.send(ctx, http.MethodPost, "/v1/worktrees/setup", WorktreeRequest{Path: path, Again: again}, &st)
	return st, err
}

// WorktreeSetup reports the setup of the worktree at path.
func (c *Client) WorktreeSetup(ctx context.Context, path string) (WorktreeStatus, error) {
	var st WorktreeStatus
	err := c.get(ctx, "/v1/worktrees/setup?path="+url.QueryEscape(path), &st)
	return st, err
}

// RemoveWorktree runs the worktree's before_delete hook and removes it with git.
func (c *Client) RemoveWorktree(ctx context.Context, path string, force bool) (string, error) {
	var out WorktreeRemoval
	err := c.timeout(10*time.Minute).send(ctx, http.MethodPost, "/v1/worktrees/remove", WorktreeRequest{Path: path, Force: force}, &out)
	return out.Output, err
}
