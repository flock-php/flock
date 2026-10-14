package postgres

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
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

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMigrateLegacyDataDirMovesClusterToItsMajor(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "services", "postgres", "data")
	fakeCluster(t, legacy, "17")
	// A stale pid file from a crash: the process is gone.
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(legacy, "postmaster.pid"), []byte(strconv.Itoa(dead.Process.Pid)+"\n/data\n"), 0o600)

	if err := MigrateLegacyDataDir(root); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(DataDir(root, "17"), "base", "1", "1259")); got != "table data" {
		t.Fatalf("data not moved: %q", got)
	}
	if major, _ := ClusterMajor(DataDir(root, "17")); major != "17" {
		t.Fatalf("major %q", major)
	}
	if HasCluster(DataRoot(root)) {
		t.Fatal("legacy cluster still in place")
	}
	if _, err := os.Stat(migratingDir(root)); !os.IsNotExist(err) {
		t.Fatal("staging dir left behind")
	}

	// Idempotent.
	if err := MigrateLegacyDataDir(root); err != nil {
		t.Fatal(err)
	}
	if !HasCluster(DataDir(root, "17")) {
		t.Fatal("cluster lost on second run")
	}
}

func TestMigrateLegacyDataDirNothingToDo(t *testing.T) {
	root := t.TempDir()
	if err := MigrateLegacyDataDir(root); err != nil {
		t.Fatal(err)
	}
	fakeCluster(t, DataDir(root, "18"), "18")
	if err := MigrateLegacyDataDir(root); err != nil {
		t.Fatal(err)
	}
	if !HasCluster(DataDir(root, "18")) {
		t.Fatal("cluster gone")
	}
}

func TestMigrateLegacyDataDirResumesInterruptedMove(t *testing.T) {
	root := t.TempDir()
	fakeCluster(t, migratingDir(root), "16")
	if err := MigrateLegacyDataDir(root); err != nil {
		t.Fatal(err)
	}
	if !HasCluster(DataDir(root, "16")) {
		t.Fatal("not resumed")
	}
}

func TestMigrateLegacyDataDirNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	fakeCluster(t, migratingDir(root), "18")
	fakeCluster(t, DataDir(root, "18"), "18")
	_ = os.WriteFile(filepath.Join(DataDir(root, "18"), "mine"), []byte("x"), 0o600)

	err := MigrateLegacyDataDir(root)
	if err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Fatalf("want a conflict, got %v", err)
	}
	if !HasCluster(migratingDir(root)) || mustRead(t, filepath.Join(DataDir(root, "18"), "mine")) != "x" {
		t.Fatal("data touched")
	}
}

func TestMigrateLegacyDataDirRefusesWhileRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses this process as the running postgres")
	}
	root := t.TempDir()
	legacy := DataRoot(root)
	fakeCluster(t, legacy, "18")
	_ = os.WriteFile(filepath.Join(legacy, "postmaster.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)

	err := MigrateLegacyDataDir(root)
	if err == nil || !strings.Contains(err.Error(), "still runs") {
		t.Fatalf("want refusal, got %v", err)
	}
	if !HasCluster(legacy) {
		t.Fatal("cluster moved while running")
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
