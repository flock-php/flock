package api

import (
	"errors"
	"net/http"

	"flock/internal/mcpclients"
)

// MCP is the Settings → AI pane's data: the AI clients Flock's MCP server can be registered with,
// and what to paste into any other client.
type MCP struct {
	Clients []mcpclients.Client `json:"clients"`
	Command string              `json:"command"`
	Args    []string            `json:"args"`
	Snippet string              `json:"snippet"`
}

// mcpEnv is where the AI clients live; tests replace it.
var mcpEnv = mcpclients.DefaultEnv

func registerMCP(mux *http.ServeMux, b Backend) {
	respond := func(w http.ResponseWriter, change func(env mcpclients.Env) error) {
		env, err := mcpEnv(b.Root())
		if err == nil && change != nil {
			err = change(env)
		}
		if err != nil {
			code := http.StatusInternalServerError
			switch {
			case errors.Is(err, mcpclients.ErrUnknown):
				code = http.StatusNotFound
			case errors.Is(err, mcpclients.ErrNotInstalled):
				code = http.StatusBadRequest
			}
			writeError(w, code, err)
			return
		}
		writeJSON(w, http.StatusOK, MCP{Clients: mcpclients.List(env), Command: env.Command, Args: env.Args, Snippet: mcpclients.Snippet(env)})
	}

	mux.HandleFunc("GET /v1/mcp", func(w http.ResponseWriter, _ *http.Request) { respond(w, nil) })
	mux.HandleFunc("POST /v1/mcp/clients/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, func(env mcpclients.Env) error { return mcpclients.Register(env, r.PathValue("id")) })
	})
	mux.HandleFunc("DELETE /v1/mcp/clients/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, func(env mcpclients.Env) error { return mcpclients.Unregister(env, r.PathValue("id")) })
	})
}
