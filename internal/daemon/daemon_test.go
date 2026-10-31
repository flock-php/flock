package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/binaries"
	"flock/internal/config"
	"flock/internal/paths"
)

// TestMain keeps the daemons offline: without the package manifest, the first run doesn't
// download PHP and Node.js, and versions count as offered.
func TestMain(m *testing.M) {
	binaries.Bases = nil
	os.Exit(m.Run())
}

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

// freePort returns a port that's free for TCP and UDP: Caddy also listens for HTTP/3 over
// UDP, and Windows can reserve a port for UDP that's free for TCP.
func freePort(t *testing.T) int {
	t.Helper()

	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		pc, err := net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port))
		ln.Close()
		if err == nil {
			pc.Close()

			return port
		}
	}
	t.Fatal("no port is free for both TCP and UDP")

	return 0
}

// testRoot writes a config.json that only uses free high ports.
func testRoot(t *testing.T, mutate func(*config.Config)) (string, *config.Config) {
	t.Helper()

	root := shortTempDir(t)
	cfg := config.Default()
	cfg.DefaultPHP, cfg.DefaultNode = "8.5", "22" // as after the first run
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

func TestStatusListsValkeyOnce(t *testing.T) {
	root, _ := testRoot(t, func(c *config.Config) {
		c.Services.SetExtra("valkey", config.Extra{Version: "9.1.2", Port: freePort(t)})
	})
	client := startDaemon(t, root)

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}

	var listed struct {
		Valkey string `json:"valkey"`
		Extras []struct {
			ID string `json:"id"`
		} `json:"extras"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}

	count := 0
	if listed.Valkey != "" {
		count++
	}
	for _, x := range listed.Extras {
		if x.ID == "valkey" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("valkey listed %d times: %s", count, body)
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

func TestReloadReadsTheMachinesAddressesOnce(t *testing.T) {
	var reads atomic.Int32
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		reads.Add(1)
		return []net.Addr{&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })

	root, _ := testRoot(t, func(c *config.Config) { c.Listen = []string{"100.64.0.9"} })
	client := startDaemon(t, root)

	before := reads.Load()
	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// DNS, web, the warnings and the status come from one reading: read separately, a VPN address
	// appearing in between was served by web but not DNS, with no warning to make the daemon retry.
	if n := reads.Load() - before; n != 1 {
		t.Errorf("a reload read the addresses %d times, want 1", n)
	}
	if n := countContaining(status.Warnings, "100.64.0.9 isn't an address of this machine"); n != 1 {
		t.Errorf("warning reported %d times: %q", n, status.Warnings)
	}
}

func countContaining(list []string, part string) int {
	n := 0
	for _, s := range list {
		if strings.Contains(s, part) {
			n++
		}
	}
	return n
}

func TestTheFirstRunWaitsForTheDefaultsAndSaysSo(t *testing.T) {
	root, cfg := testRoot(t, func(cfg *config.Config) { cfg.DefaultPHP, cfg.DefaultNode = "", "" })
	cfg.Upsert(config.Site{Name: "app", Path: t.TempDir()})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	client := startDaemon(t, root)

	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Offline there is no manifest yet to pick the versions from.
	if d := status.Defaults; d == nil || d.PHP.State != api.DefaultWaiting || d.Node.State != api.DefaultWaiting {
		t.Fatalf("defaults: %+v", status.Defaults)
	}
	if len(status.Sites) != 1 || status.Sites[0].Problem != "PHP is being installed" {
		t.Fatalf("sites: %+v", status.Sites)
	}

	// The site's name gets the page that says PHP is coming, not "not linked". The first run's
	// background work reloads the web server meanwhile, which can drop a connection.
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "app.test"
	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for resp, err = http.DefaultClient.Do(req); err != nil; resp, err = http.DefaultClient.Do(req) {
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Installing PHP") || strings.Contains(string(body), "Not linked") {
		t.Fatalf("got %d %q", resp.StatusCode, body)
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
		// Only the web server's warning matters: on Windows there is also one about the hosts helper.
		if !containsText(status.Warnings, "web:") {
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

func TestFoldersInParkedPathsAreFollowed(t *testing.T) {
	projectWatchInterval = 50 * time.Millisecond
	t.Cleanup(func() { projectWatchInterval = 3 * time.Second })

	parked := t.TempDir()
	root, _ := testRoot(t, func(cfg *config.Config) { cfg.Parked = []string{parked} })
	client := startDaemon(t, root)

	hasSite := func(name string) bool {
		status, _ := client.Status(context.Background())
		for _, s := range status.Sites {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	waitFor := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for hasSite("acme") != want {
			if time.Now().After(deadline) {
				t.Fatalf("acme listed = %v, want %v", !want, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	if err := os.Mkdir(filepath.Join(parked, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor(true)

	if err := os.Remove(filepath.Join(parked, "acme")); err != nil {
		t.Fatal(err)
	}
	waitFor(false)
}

func containsText(list []string, needle string) bool {
	for _, s := range list {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
