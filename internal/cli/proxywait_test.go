package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
)

// statusDaemon answers /v1/status with each of statuses in turn, then the last one again.
func statusDaemon(t *testing.T, statuses ...api.Status) *api.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "flk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "api.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("no unix sockets here: %v", err)
	}
	var mu sync.Mutex
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		st := statuses[0]
		if len(statuses) > 1 {
			statuses = statuses[1:]
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(st)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return api.NewClient(socket)
}

func proxyWith(state, problem string) api.Status {
	return api.Status{Proxies: []api.ProxyStatus{{Name: "admin", URL: "http://admin.test",
		Command: &api.ProxyCommand{Command: "docker compose up -d", State: state, Problem: problem}}}}
}

func runWait(t *testing.T, client *api.Client, start bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetContext(context.Background())
	err := waitForProxy(cmd, client, "admin", start, 3*time.Second)
	return out.String(), err
}

func TestWaitingForAProxysServer(t *testing.T) {
	if out, err := runWait(t, statusDaemon(t, proxyWith(api.ProxyStarting, ""), proxyWith(api.ProxyRunning, "")), true); err != nil || !strings.Contains(out, "started admin") {
		t.Errorf("start: %q %v", out, err)
	}
	// A background stop command runs, then is done.
	if out, err := runWait(t, statusDaemon(t, proxyWith(api.ProxyStopping, ""), proxyWith(api.ProxyStopped, "")), false); err != nil || !strings.Contains(out, "stopped admin") {
		t.Errorf("stop: %q %v", out, err)
	}
	// A failing stop command is an error, not "stopped".
	if _, err := runWait(t, statusDaemon(t, proxyWith(api.ProxyStopping, ""), proxyWith(api.ProxyFailed, "`docker compose stop` failed")), false); err == nil || !strings.Contains(err.Error(), "docker compose stop") {
		t.Errorf("failed stop: %v", err)
	}
	// The proxy removed meanwhile: no endless wait.
	if _, err := runWait(t, statusDaemon(t, proxyWith(api.ProxyStarting, ""), api.Status{}), true); err == nil || !strings.Contains(err.Error(), "no longer has a command") {
		t.Errorf("removed: %v", err)
	}
	// Still starting at the deadline: says so and returns.
	start := time.Now()
	if out, err := runWait(t, statusDaemon(t, proxyWith(api.ProxyStarting, "")), true); err != nil || !strings.Contains(out, "still starting") || time.Since(start) > 10*time.Second {
		t.Errorf("deadline: %q %v", out, err)
	}
}
