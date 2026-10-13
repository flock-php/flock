package postgres

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flock/internal/supervisor"
)

// TestIntegrationInstallRunCreate downloads real binaries (~70MB). Run with:
// FLOCK_PG_INTEGRATION=1 go test ./internal/postgres -run Integration -v
func TestIntegrationInstallRunCreate(t *testing.T) {
	if testing.Short() || os.Getenv("FLOCK_PG_INTEGRATION") != "1" {
		t.Skip("set FLOCK_PG_INTEGRATION=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Short root keeps the unix socket path under the macOS limit.
	root, err := os.MkdirTemp("", "pg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)

	inst, err := NewInstaller(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Install(ctx, DefaultVersion); err != nil {
		t.Fatal(err)
	}

	data := filepath.Join(root, "data")
	if err := InitDB(inst.BinDir(DefaultVersion), data); err != nil {
		t.Fatal(err)
	}
	if err := InitDB(inst.BinDir(DefaultVersion), data); err != nil {
		t.Fatalf("second InitDB must be a no-op: %v", err)
	}

	port := freePort(t)
	runDir := filepath.Join(root, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}

	sup := supervisor.New()
	defer sup.StopAll()
	sup.Ensure(Spec(inst.BinDir(DefaultVersion), data, runDir, filepath.Join(root, "pg.log"), port))

	deadline := time.Now().Add(30 * time.Second)
	for Exec(ctx, port, "postgres", "SELECT 1") != nil {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(root, "pg.log"))
			t.Fatalf("postgres did not start:\n%s", log)
		}
		time.Sleep(200 * time.Millisecond)
	}

	for range 2 {
		if err := CreateDatabase(ctx, port, "watchtower_testing"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Exec(ctx, port, "watchtower_testing", "CREATE TABLE t (id int); INSERT INTO t VALUES (1); SELECT * FROM t"); err != nil {
		t.Fatal(err)
	}

	var pgErr *Error
	if err := Exec(ctx, port, "watchtower_testing", "SELECT * FROM missing"); err == nil || !errors.As(err, &pgErr) || pgErr.Code != "42P01" {
		t.Fatalf("want undefined_table error, got %v", err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port
}
