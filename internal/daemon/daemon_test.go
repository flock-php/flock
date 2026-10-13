package daemon

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"flock/internal/api"
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

func startDaemon(t *testing.T, root string) (*api.Client, chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Root: root, TLD: "test", DNSAddr: "127.0.0.1:0", Version: "dev"})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	client := api.NewClient(paths.Socket(root))
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := client.Status(context.Background()); err == nil {
			return client, done
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStatusReportsRunningServices(t *testing.T) {
	client, _ := startDaemon(t, shortTempDir(t))

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.PID != os.Getpid() || status.Version != "dev" || status.DNS.TLD != "test" || status.DNS.Addr == "" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestRefusesToStartTwice(t *testing.T) {
	root := shortTempDir(t)
	startDaemon(t, root)

	err := Run(context.Background(), Config{Root: root, TLD: "test", DNSAddr: "127.0.0.1:0"})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("want ErrAlreadyRunning, got %v", err)
	}
}

func TestRemovesStaleSocket(t *testing.T) {
	root := shortTempDir(t)
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
