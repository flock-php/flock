package web

import (
	"net/http"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// WakeFunc is told of every request to a Node.js site (see Site.Node) before it passes to the
// site's dev server, which it starts when it isn't running. It returns the server's state and
// what went wrong, for the page shown while the server doesn't answer, and done, called when the
// request is over: for a WebSocket (hot reload), once the connection closes.
type WakeFunc func(site string) (state, problem string, done func())

// currentWake is Config.Wake of the config Caddy runs.
var currentWake atomic.Pointer[WakeFunc]

func init() {
	caddy.RegisterModule(wakeHandler{})
	httpcaddyfile.RegisterHandlerDirective("flock_wake", parseWakeHandler)
	httpcaddyfile.RegisterDirectiveOrder("flock_wake", httpcaddyfile.Before, "reverse_proxy")
}

// wakeHandler tells the daemon of a request to a Node.js site, and sets {http.vars.flock_node_state}
// and {http.vars.flock_node_problem} for the page shown while its dev server doesn't answer.
type wakeHandler struct {
	Site string `json:"site"`
}

func (wakeHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.flock_wake",
		New: func() caddy.Module { return new(wakeHandler) },
	}
}

func parseWakeHandler(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var s wakeHandler
	err := s.UnmarshalCaddyfile(h.Dispenser)
	return &s, err
}

var _ caddyfile.Unmarshaler = (*wakeHandler)(nil)

// UnmarshalCaddyfile reads `flock_wake <site>`.
func (s *wakeHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	if !d.NextArg() {
		return d.ArgErr()
	}
	s.Site = d.Val()
	if d.NextArg() {
		return d.ArgErr()
	}
	return nil
}

func (s wakeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if wake := currentWake.Load(); wake != nil {
		state, problem, done := (*wake)(s.Site)
		if done != nil {
			// reverse_proxy returns once an upgraded connection is closed.
			defer done()
		}
		caddyhttp.SetVar(r.Context(), "flock_node_state", state)
		caddyhttp.SetVar(r.Context(), "flock_node_problem", problem)
	}
	return next.ServeHTTP(w, r)
}
