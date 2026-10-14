package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateWindowsRedisMovesBinariesAndData(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "services", "valkey")
	touch(t, filepath.Join(old, "7.2.16", "valkey-server.exe"))
	touch(t, filepath.Join(old, "7.2.16", "valkey-cli.exe"))
	touch(t, filepath.Join(old, "7.2.16", "msys-2.0.dll"))
	touch(t, filepath.Join(old, "data", "dump.rdb"))

	if err := migrateWindowsRedis(root); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		filepath.Join(Dir(root, "redis", "7.2.16"), "redis-server.exe"),
		filepath.Join(Dir(root, "redis", "7.2.16"), "redis-cli.exe"),
		filepath.Join(Dir(root, "redis", "7.2.16"), "msys-2.0.dll"),
		filepath.Join(DataDir(root, "redis", windowsRedisLine), "dump.rdb"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("missing %s", want)
		}
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old directory kept")
	}
	if err := migrateWindowsRedis(root); err != nil {
		t.Fatalf("second run: %v", err)
	}
}
