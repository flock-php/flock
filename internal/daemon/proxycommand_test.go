package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
)

// testCommand is the test binary run as a proxy's command (see TestMain):
//   - flock-test-server serves on $PORT, answering with the Host, and logs each request;
//   - flock-test-exit says why it can't start and exits 1;
//   - flock-test-background starts flock-test-server and exits, leaving its pid in server.pid;
//   - flock-test-stop stops the server server.pid names.
func testCommand(mode string) int {
	switch mode {
	case "flock-test-server":
		ln, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
		if err != nil {
			fmt.Println(err)
			return 1
		}
		fmt.Println("listening on", ln.Addr())
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Println("request", r.Host, r.URL.Path)
			if r.Header.Get("Upgrade") == "websocket" {
				// Hot reload's WebSocket: open until the browser closes it.
				conn, rw, err := http.NewResponseController(w).Hijack()
				if err != nil {
					return
				}
				defer conn.Close()
				rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				rw.Flush()
				_, _ = io.Copy(io.Discard, rw)
				return
			}
			io.WriteString(w, "dev server for "+r.Host)
		}))
		return 0
	case "flock-test-exit":
		fmt.Println("Error: Cannot find module 'next'")
		return 1
	case "flock-test-background":
		server := exec.Command(os.Args[0], "flock-test-server")
		server.Stdout, server.Stderr = os.Stdout, os.Stderr
		if err := server.Start(); err != nil {
			fmt.Println(err)
			return 1
		}
		if err := os.WriteFile("server.pid", []byte(strconv.Itoa(server.Process.Pid)), 0o600); err != nil {
			return 1
		}
		fmt.Println("started in the background")
		return 0
	case "flock-test-stop":
		data, err := os.ReadFile("server.pid")
		if err != nil {
			fmt.Println(err)
			return 1
		}
		pid, _ := strconv.Atoi(string(data))
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
		fmt.Println("stopped")
		return 0
	}
	return 2
}

// testCommandLine runs the test binary as a command in mode, through the shell.
func testCommandLine(mode string) string {
	return `"` + os.Args[0] + `" ` + mode
}

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

// waitProxy waits until the proxy's command is in state.
func waitProxy(t *testing.T, client *api.Client, name, state string) api.ProxyStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		p := proxyStatus(t, client, name)
		if p.Command != nil && p.Command.State == state {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s isn't %s: %+v", name, state, p.Command)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func get(t *testing.T, cfg *config.Config, host string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/page", nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func readLog(t *testing.T, root, name string) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(root, "log", name+".log"))
	return string(data)
}

func TestAProxysCommandRunsFromStartToStop(t *testing.T) {
	port := freePort(t)
	root, cfg := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{{Name: "app", Upstream: "http://127.0.0.1:" + strconv.Itoa(port),
			Command: testCommandLine("flock-test-server"), Dir: t.TempDir()}}
	})
	client := startDaemon(t, root)

	// Not started: the command waits for a click.
	p := proxyStatus(t, client, "app")
	if p.Command == nil || p.Command.State != api.ProxyStopped || p.Running || p.Command.Log != "proxy-app" {
		t.Fatalf("before the start: %+v %+v", p, p.Command)
	}

	if _, err := client.StartProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	p = waitProxy(t, client, "app", api.ProxyRunning)
	if !p.Running {
		t.Fatalf("running but not answering: %+v", p)
	}
	if code, body := get(t, cfg, "app.test"); code != http.StatusOK || body != "dev server for app.test" {
		t.Fatalf("through the proxy: %d %q", code, body)
	}
	if log := readLog(t, root, "proxy-app"); !strings.Contains(log, "listening on 127.0.0.1:"+strconv.Itoa(port)) || !strings.Contains(log, "request app.test /page") {
		t.Fatalf("the output isn't in the log:\n%s", log)
	}

	// Starting it again changes nothing.
	if _, err := client.StartProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}

	if _, err := client.StopProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if p := proxyStatus(t, client, "app"); p.Command.State != api.ProxyStopped || p.Running {
		t.Fatalf("after the stop: %+v %+v", p, p.Command)
	}
	if answers("http://127.0.0.1:" + strconv.Itoa(port)) {
		t.Fatal("the server still holds its port")
	}
}

func TestRemovingAStartedProxyStopsItsServer(t *testing.T) {
	port := freePort(t)
	root, _ := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{{Name: "app", Upstream: "http://127.0.0.1:" + strconv.Itoa(port),
			Command: testCommandLine("flock-test-server"), Dir: t.TempDir()}}
	})
	client := startDaemon(t, root)
	if _, err := client.StartProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	waitProxy(t, client, "app", api.ProxyRunning)

	if _, err := client.RemoveProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if answers("http://127.0.0.1:" + strconv.Itoa(port)) {
		t.Fatal("the removed proxy's server still runs")
	}
}

func TestAProxysCommandThatExitsSaysWhy(t *testing.T) {
	root, _ := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{{Name: "app", Upstream: "http://127.0.0.1:" + strconv.Itoa(freePort(t)),
			Command: testCommandLine("flock-test-exit"), Dir: t.TempDir()}}
	})
	client := startDaemon(t, root)
	if _, err := client.StartProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	p := waitProxy(t, client, "app", api.ProxyFailed)
	if !strings.Contains(p.Command.Problem, "Cannot find module 'next'") {
		t.Fatalf("problem: %q", p.Command.Problem)
	}
	if _, err := client.StopProxy(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if p := proxyStatus(t, client, "app"); p.Command.State != api.ProxyStopped {
		t.Fatalf("after the stop: %+v", p.Command)
	}
}

func TestABackgroundCommandIsStartedAndStoppedByItsCommands(t *testing.T) {
	port := freePort(t)
	upstream := "http://127.0.0.1:" + strconv.Itoa(port)
	root, _ := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{{Name: "s3", Upstream: upstream, Dir: t.TempDir(),
			Command: testCommandLine("flock-test-background"), StopCommand: testCommandLine("flock-test-stop")}}
	})
	client := startDaemon(t, root)
	t.Cleanup(func() { _, _ = client.StopProxy(context.Background(), "s3") })

	if _, err := client.StartProxy(context.Background(), "s3"); err != nil {
		t.Fatal(err)
	}
	// The command has exited; the server it started runs on.
	waitProxy(t, client, "s3", api.ProxyRunning)
	if log := readLog(t, root, "proxy-s3"); !strings.Contains(log, "started in the background") {
		t.Fatalf("log:\n%s", log)
	}

	if _, err := client.StopProxy(context.Background(), "s3"); err != nil {
		t.Fatal(err)
	}
	waitProxy(t, client, "s3", api.ProxyStopped)
	deadline := time.Now().Add(5 * time.Second)
	for answers(upstream) {
		if time.Now().After(deadline) {
			t.Fatal("the stop command didn't stop the server")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStartingAProxyIsRefusedWithoutACommandOrWhenItsPortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	root, _ := testRoot(t, func(c *config.Config) {
		c.Proxies = []config.Proxy{
			{Name: "plain", Upstream: "http://127.0.0.1:3000"},
			{Name: "busy", Upstream: "http://" + taken.Addr().String(), Command: testCommandLine("flock-test-server"), Dir: t.TempDir()},
		}
	})
	client := startDaemon(t, root)

	if _, err := client.StartProxy(context.Background(), "plain"); err == nil || !strings.Contains(err.Error(), "has no command") {
		t.Fatalf("a proxy without a command: %v", err)
	}
	if _, err := client.StartProxy(context.Background(), "busy"); err == nil || !strings.Contains(err.Error(), "already answers") {
		t.Fatalf("a taken port: %v", err)
	}
}
