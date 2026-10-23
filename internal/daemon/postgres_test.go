package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"flock/internal/config"
	"flock/internal/paths"
	"flock/internal/postgres"
)

// On unix Postgres puts its socket and lock file in run/. Without a site, no PHP-FPM pool has
// created it. Windows connects over TCP and has no run/.
func TestPostgresCreatesTheSocketDirectory(t *testing.T) {
	root := shortTempDir(t)
	const id = "18.6-build1"
	exe := "postgres"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	for _, file := range []string{
		filepath.Join(postgres.BinDir(root, id), exe),
		filepath.Join(postgres.DataDir(root, postgres.MajorOf(id)), "PG_VERSION"), // skips initdb
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	d := &daemon{root: root}
	if _, err := d.postgresSpec(config.Default(), id); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths.Run(root))
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatal("run/ was created on Windows")
		}
		return
	}
	if err != nil || !info.IsDir() {
		t.Fatalf("run/ was not created: %v", err)
	}
}
