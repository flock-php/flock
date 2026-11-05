package daemon

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
)

// proxyStatus is the live status of the proxy named name.
func proxyStatus(t *testing.T, client *api.Client, name string) api.ProxyStatus {
	t.Helper()
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range st.Proxies {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no proxy %s in %+v", name, st.Proxies)
	return api.ProxyStatus{}
}

// waitForProxy waits until the proxy's command is in state.
func waitForProxy(t *testing.T, client *api.Client, name, state string) api.ProxyStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		p := proxyStatus(t, client, name)
		if p.Command != nil && p.Command.State == state {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s isn't %s: %+v %+v", name, state, p, p.Command)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// fakeServer is the test binary as node in root (see installFakeNode), quoted for the shell: it
// serves $PORT until it's stopped.
func fakeServer(t *testing.T, root string) string {
	t.Helper()
	installFakeNode(t, root)
	return `"` + filepath.Join(root, "node", "22.1.0", "bin", "node") + `"`
}

func TestAProxyRunsItsCommandWhileItsStarted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command runs through /bin/sh here")
	}
	project, port := t.TempDir(), freePort(t)
	var server string
	root, cfg := testRoot(t, func(c *config.Config) {})
	server = fakeServer(t, root)
	if err := cfg.UpsertProxy(config.Proxy{Name: "softpack", Upstream: "http://127.0.0.1:" + strconv.Itoa(port), Command: server + " dev", Dir: project}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	client := startDaemon(t, root)

	// It waits to be started.
	p := proxyStatus(t, client, "softpack")
	if p.Command == nil || p.Command.State != api.ProxyStopped || p.Command.Dir != project || p.Command.Log != "proxy-softpack" || p.Running {
		t.Fatalf("not started yet: %+v %+v", p, p.Command)
	}

	if _, err := client.StartProxy(context.Background(), "softpack"); err != nil {
		t.Fatal(err)
	}
	waitForProxy(t, client, "softpack", api.ProxyRunning)

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "softpack.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), "node softpack.test dev ") {
		t.Fatalf("got %q", body)
	}
	if log, _ := os.ReadFile(filepath.Join(root, "log", "proxy-softpack.log")); !strings.Contains(string(log), "starting proxy-softpack") {
		t.Errorf("the command's log: %q", log)
	}

	// What Stop answers is up to date: the upstream doesn't answer any more.
	st, err := client.StopProxy(context.Background(), "softpack")
	if err != nil {
		t.Fatal(err)
	}
	if p := st.Proxies[0]; p.Command.State != api.ProxyStopped || p.Running {
		t.Errorf("stopped: %+v %+v", p, p.Command)
	}
	if !portFree(port) {
		t.Errorf("port %d is still taken once stopped", port)
	}

	// A started proxy that's removed stops too.
	if _, err := client.StartProxy(context.Background(), "softpack"); err != nil {
		t.Fatal(err)
	}
	waitForProxy(t, client, "softpack", api.ProxyRunning)
	if _, err := client.RemoveProxy(context.Background(), "softpack"); err != nil {
		t.Fatal(err)
	}
	if !portFree(port) {
		t.Errorf("port %d is still taken once the proxy is removed", port)
	}
}

func TestAProxyStartsAndStopsAServerInTheBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the commands run through /bin/sh here")
	}
	project, port := t.TempDir(), freePort(t)
	root, cfg := testRoot(t, func(c *config.Config) {})
	server := fakeServer(t, root)
	if err := cfg.UpsertProxy(config.Proxy{
		Name: "s3", Upstream: "http://127.0.0.1:" + strconv.Itoa(port), Dir: project,
		Command:     server + " serve >/dev/null 2>&1 & echo $! > server.pid",
		StopCommand: `kill "$(cat server.pid)"`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	client := startDaemon(t, root)

	if _, err := client.StartProxy(context.Background(), "s3"); err != nil {
		t.Fatal(err)
	}
	waitForProxy(t, client, "s3", api.ProxyRunning)
	// The command exited, but the server it started runs on.
	if _, err := os.Stat(filepath.Join(project, "server.pid")); err != nil {
		t.Fatal(err)
	}

	if _, err := client.StopProxy(context.Background(), "s3"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !portFree(port) {
		if time.Now().After(deadline) {
			t.Fatalf("the stop command didn't stop the server on %d", port)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if p := proxyStatus(t, client, "s3"); p.Command.State != api.ProxyStopped {
		t.Errorf("stopped: %+v", p.Command)
	}
}

func TestAProxysCommandSaysWhyItFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the commands run through /bin/sh here")
	}
	project := t.TempDir()
	root, _ := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{
			{Name: "plain", Upstream: "http://127.0.0.1:1"},
			{Name: "broken", Upstream: "http://127.0.0.1:1", Command: "echo nope; exit 3", Dir: project},
			{Name: "bg", Upstream: "http://127.0.0.1:1", Command: "exit 4", StopCommand: "true", Dir: project},
		}
	})
	client := startDaemon(t, root)

	if _, err := client.StartProxy(context.Background(), "plain"); err == nil || !strings.Contains(err.Error(), "no command") {
		t.Errorf("a proxy without a command: %v", err)
	}

	if _, err := client.StartProxy(context.Background(), "broken"); err != nil {
		t.Fatal(err)
	}
	if p := waitForProxy(t, client, "broken", api.ProxyFailed); !strings.Contains(p.Command.Problem, "flock logs proxy-broken") {
		t.Errorf("why: %q", p.Command.Problem)
	}
	if log, _ := os.ReadFile(filepath.Join(root, "log", "proxy-broken.log")); !strings.Contains(string(log), "nope") {
		t.Errorf("the command's output: %q", log)
	}

	if _, err := client.StartProxy(context.Background(), "bg"); err != nil {
		t.Fatal(err)
	}
	if p := waitForProxy(t, client, "bg", api.ProxyFailed); !strings.Contains(p.Command.Problem, "`exit 4` failed") {
		t.Errorf("why: %q", p.Command.Problem)
	}
}
