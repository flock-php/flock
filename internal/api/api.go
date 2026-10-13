// Package api is the local HTTP API between the Flock daemon and its clients
// (the CLI now, the native macOS/Windows apps later). It is served over a unix socket.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"flock/internal/supervisor"
)

// ErrDaemonNotRunning is returned when nothing is listening on the socket.
var ErrDaemonNotRunning = errors.New("flock daemon is not running (start it with `flock daemon`)")

// Status describes the running daemon.
type Status struct {
	Version   string                  `json:"version"`
	PID       int                     `json:"pid"`
	StartedAt time.Time               `json:"started_at"`
	DNS       DNSStatus               `json:"dns"`
	HTTPPort  int                     `json:"http_port"`
	HTTPSPort int                     `json:"https_port"`
	Mail      string                  `json:"mail"`
	Dumps     bool                    `json:"dumps"`
	MailCount int                     `json:"mail_count"`
	DumpCount int                     `json:"dump_count"`
	Postgres  string                  `json:"postgres,omitempty"`
	Redis     string                  `json:"redis,omitempty"`
	Xdebug    bool                    `json:"xdebug"`
	Sites     []SiteStatus            `json:"sites"`
	Processes []supervisor.ProcStatus `json:"processes"`
	Warnings  []string                `json:"warnings,omitempty"`
}

// SiteStatus is one served site as the daemon sees it.
type SiteStatus struct {
	Name    string `json:"name"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	PHP     string `json:"php"`
	Secured bool   `json:"secured"`
	Parked  bool   `json:"parked"`
	URL     string `json:"url,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// DNSStatus describes the embedded DNS server.
type DNSStatus struct {
	TLD  string `json:"tld"`
	Addr string `json:"addr"`
}

// Settings are the toggles a UI can change; nil fields stay as they are.
type Settings struct {
	Xdebug *bool `json:"xdebug"`
	Dumps  *bool `json:"dumps"`
}

// Backend is what the API needs from the daemon.
type Backend interface {
	Status() Status
	Reload() error
	UpdateSettings(Settings) error
	Root() string
}

// NewHandler builds the API routes. A non-empty token requires `Authorization: Bearer <token>`
// (used for the TCP listener the native apps talk to; the CLI's unix socket is protected by file permissions).
func NewHandler(b Backend, token string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := b.Reload(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/settings", func(w http.ResponseWriter, r *http.Request) {
		var settings Settings
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&settings); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid settings: %w", err))
			return
		}
		if err := b.UpdateSettings(settings); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})

	registerMail(mux, b)
	registerDumps(mux, b)
	registerLogs(mux, b)

	if token == "" {
		return mux
	}
	return requireToken(token, mux)
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("missing or invalid token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// Client talks to the daemon over its socket.
type Client struct {
	http *http.Client
}

// NewClient creates a client for the daemon listening on socket.
func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}

	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
}

// Status fetches the daemon status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.get(ctx, "/v1/status", &status)

	return status, err
}

// Reload asks the daemon to apply config.json and returns the resulting status.
func (c *Client) Reload(ctx context.Context) (Status, error) {
	var status Status
	err := c.do(ctx, http.MethodPost, "/v1/reload", &status)

	return status, err
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, out)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://flockd"+path, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrDaemonNotRunning
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error != "" {
			return errors.New(body.Error)
		}
		return fmt.Errorf("%s %s: unexpected status %s", method, path, resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
