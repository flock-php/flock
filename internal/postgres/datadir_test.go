package postgres

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeCluster creates a data directory that looks like a cluster of major.
func fakeCluster(t *testing.T, dir, major string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "base", "1"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"PG_VERSION": major + "\n", "base/1/1259": "table data", "postgresql.conf": "# conf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	fakeCluster(t, dir, "18")
	if got := DirSize(dir); got != int64(len("18\n")+len("table data")+len("# conf")) {
		t.Fatalf("got %d", got)
	}
	if DirSize(filepath.Join(dir, "missing")) != 0 {
		t.Fatal("missing dir")
	}
}
