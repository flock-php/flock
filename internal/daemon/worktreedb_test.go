package daemon

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/postgres"
	"flock/internal/supervisor"
	"flock/internal/worktree"
)

// TestPostgresWorktreeDatabases runs against installed binaries:
// FLOCK_PG_BIN=~/Library/Application\ Support/Flock/services/postgres/18.6-build1/bin go test ./internal/daemon -run PostgresWorktree -v
func TestPostgresWorktreeDatabases(t *testing.T) {
	bin := os.Getenv("FLOCK_PG_BIN")
	if bin == "" {
		t.Skip("set FLOCK_PG_BIN to a postgres bin directory to run")
	}
	root := shortTempDir(t)
	data := filepath.Join(root, "data")
	if err := postgres.InitDB(bin, data); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	sup := supervisor.New()
	sup.Ensure(postgres.Spec(bin, data, root, filepath.Join(root, "pg.log"), port))
	t.Cleanup(sup.StopAll)

	ctx := context.Background()
	deadline := time.Now().Add(30 * time.Second)
	for postgres.Exec(ctx, port, "postgres", "SELECT 1") != nil {
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(root, "pg.log"))
			t.Fatalf("postgres did not start:\n%s", log)
		}
		time.Sleep(200 * time.Millisecond)
	}
	psql := func(db, sql string) string {
		t.Helper()
		out, err := exec.Command(filepath.Join(bin, exe("psql")), "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", "postgres", "-tA", "-d", db, "-c", sql).CombinedOutput()
		if err != nil {
			t.Fatalf("psql %s: %v\n%s", sql, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := postgres.CreateDatabase(ctx, port, "acme"); err != nil {
		t.Fatal(err)
	}
	psql("acme", "CREATE TABLE users (name text); INSERT INTO users VALUES ('ada')")

	s := pgServer{bin: bin, port: port}
	var log bytes.Buffer
	if err := s.Copy(ctx, "acme", "acme_feature", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if got := psql("acme_feature", "SELECT name FROM users"); got != "ada" {
		t.Errorf("the copy has %q", got)
	}
	if err := s.Copy(ctx, "acme", "acme_feature", &log); !errors.Is(err, worktree.ErrExists) {
		t.Errorf("copying onto a database that exists: %v", err)
	}
	if err := s.Drop(ctx, "acme_feature"); err != nil {
		t.Fatal(err)
	}

	// While the site's app is connected, the template can't be used: dumped and restored.
	busy := exec.Command(filepath.Join(bin, exe("psql")), "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", "postgres", "-d", "acme", "-c", "SELECT pg_sleep(20)")
	if err := busy.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = busy.Process.Kill(); _ = busy.Wait() })
	for psql("postgres", "SELECT count(*) FROM pg_stat_activity WHERE datname = 'acme'") == "0" {
		time.Sleep(50 * time.Millisecond)
	}
	log.Reset()
	if err := s.Copy(ctx, "acme", "acme_busy", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "dumped and restored") || psql("acme_busy", "SELECT name FROM users") != "ada" {
		t.Errorf("no dump and restore:\n%s", log.String())
	}
	if err := s.Create(ctx, "acme_busy"); !errors.Is(err, worktree.ErrExists) {
		t.Errorf("creating a database that exists: %v", err)
	}
}
