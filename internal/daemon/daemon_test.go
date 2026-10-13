package daemon

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/paths"
)

// shortTempDir keeps the socket path under macOS's ~104 byte AF_UNIX limit,
// which t.TempDir() under /var/folders can exceed.
func shortTempDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "flk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	return ln.Addr().(*net.TCPAddr).Port
}

// testRoot writes a config.json that only uses free high ports.
func testRoot(t *testing.T, mutate func(*config.Config)) (string, *config.Config) {
	t.Helper()

	root := shortTempDir(t)
	cfg := config.Default()
	cfg.Ports = config.Ports{HTTP: freePort(t), HTTPS: freePort(t), DNS: freePort(t), SMTP: freePort(t), Postgres: freePort(t)}
	if mutate != nil {
		mutate(cfg)
	}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	return root, cfg
}

func startDaemon(t *testing.T, root string) *api.Client {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Config{Root: root, Version: "dev"}) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := api.NewClient(paths.Socket(root))
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := client.Status(context.Background()); err == nil {
			return client
		}
		select {
		case err := <-done:
			t.Fatalf("daemon exited: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStatusReportsRunningServices(t *testing.T) {
	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.PID != os.Getpid() || status.Version != "dev" || status.DNS.TLD != "test" || status.Mail == "" || status.HTTPPort != cfg.Ports.HTTP {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestUnknownHostsGetTheCatchAllPage(t *testing.T) {
	root, cfg := testRoot(t, nil)
	startDaemon(t, root)

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "nothing.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "nothing.test") {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestReloadReportsSitesWithMissingPHP(t *testing.T) {
	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), PHP: "7.0"})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Sites) != 1 || status.Sites[0].Problem != "PHP 7.0 is not installed" {
		t.Fatalf("unexpected sites: %+v", status.Sites)
	}
}

func TestRefusesToStartTwice(t *testing.T) {
	root, _ := testRoot(t, nil)
	startDaemon(t, root)

	err := Run(context.Background(), Config{Root: root})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestRemovesStaleSocket(t *testing.T) {
	root, _ := testRoot(t, nil)
	if err := os.WriteFile(paths.Socket(root), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	startDaemon(t, root)
}

func TestClientReportsDaemonNotRunning(t *testing.T) {
	client := api.NewClient(paths.Socket(shortTempDir(t)))

	if _, err := client.Status(context.Background()); !errors.Is(err, api.ErrDaemonNotRunning) {
		t.Fatalf("want ErrDaemonNotRunning, got %v", err)
	}
}

func TestBusyPortsBecomeWarningsAndRecover(t *testing.T) {
	retryInterval = 100 * time.Millisecond
	t.Cleanup(func() { retryInterval = 15 * time.Second })

	root, cfg := testRoot(t, nil)
	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP))
	if err != nil {
		t.Fatal(err)
	}

	client := startDaemon(t, root)
	status, _ := client.Status(context.Background())
	if !containsText(status.Warnings, "web: port "+strconv.Itoa(cfg.Ports.HTTP)+" is used by") {
		t.Fatalf("busy web port not reported: %v", status.Warnings)
	}

	blocker.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ = client.Status(context.Background())
		if len(status.Warnings) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("web server did not recover: %v", status.Warnings)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReloadMovesServicesToNewPorts(t *testing.T) {
	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	cfg.Ports.SMTP = freePort(t)
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Mail != "127.0.0.1:"+strconv.Itoa(cfg.Ports.SMTP) {
		t.Fatalf("mail did not move: %s (warnings %v)", status.Mail, status.Warnings)
	}
}

func containsText(list []string, needle string) bool {
	for _, s := range list {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
