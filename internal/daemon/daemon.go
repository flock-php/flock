// Package daemon runs Flock's long-lived services (DNS now; nginx, PHP, mail, dumps later)
// and exposes them through the local API.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"flock/internal/api"
	"flock/internal/dns"
	"flock/internal/paths"
)

// Config controls a daemon instance.
type Config struct {
	Root    string // data directory, see paths.Root
	TLD     string // e.g. "test"
	DNSAddr string // e.g. "127.0.0.1:53535"
	Version string
}

// ErrAlreadyRunning is returned when another daemon owns the socket.
var ErrAlreadyRunning = errors.New("flock daemon is already running")

// Run starts all services and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := os.MkdirAll(cfg.Root, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", cfg.Root, err)
	}

	socket := paths.Socket(cfg.Root)
	if err := claimSocket(socket); err != nil {
		return err
	}

	dnsServer := dns.New(cfg.DNSAddr, cfg.TLD)
	if err := dnsServer.Start(); err != nil {
		return err
	}
	defer dnsServer.Shutdown()

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	defer os.Remove(socket)

	startedAt := time.Now()
	apiServer := &http.Server{
		Handler: api.NewHandler(func() api.Status {
			return api.Status{
				Version:   cfg.Version,
				PID:       os.Getpid(),
				StartedAt: startedAt,
				DNS:       api.DNSStatus{TLD: dnsServer.TLD(), Addr: dnsServer.Addr()},
			}
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- apiServer.Serve(ln) }()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return fmt.Errorf("api server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return apiServer.Shutdown(shutdownCtx)
}

// claimSocket refuses to start when a live daemon answers on socket, and removes a stale one left by a crash.
func claimSocket(socket string) error {
	if _, err := os.Stat(socket); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}

	if err := os.Remove(socket); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", socket, err)
	}

	return nil
}
