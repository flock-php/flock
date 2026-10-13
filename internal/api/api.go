// Package api is the local HTTP API between the Flock daemon and its clients
// (the CLI now, the native macOS/Windows apps later). It is served over a unix socket.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrDaemonNotRunning is returned when nothing is listening on the socket.
var ErrDaemonNotRunning = errors.New("flock daemon is not running (start it with `flock daemon`)")

// Status describes the running daemon.
type Status struct {
	Version   string    `json:"version"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	DNS       DNSStatus `json:"dns"`
}

// DNSStatus describes the embedded DNS server.
type DNSStatus struct {
	TLD  string `json:"tld"`
	Addr string `json:"addr"`
}

// NewHandler builds the API routes. status is called per request so it always reflects live state.
func NewHandler(status func() Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, status())
	})

	return mux
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

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://flockd"+path, nil)
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
		return fmt.Errorf("GET %s: unexpected status %s", path, resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
