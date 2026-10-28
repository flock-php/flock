package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	if _, _, err := d.postgresSpec(config.Default(), id); err != nil {
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

// Installed extensions and the custom folder are linked into the server on every reload, and
// TimescaleDB is preloaded (with its telemetry off) only on the major it is installed for.
func TestPostgresSpecLinksAndPreloadsExtensions(t *testing.T) {
	root := shortTempDir(t)
	const id = "18.6-build1"
	exe, module := "postgres", ".so"
	if runtime.GOOS == "windows" {
		exe, module = "postgres.exe", ".dll"
	}
	build := postgres.Dir(root, id)
	timescale := postgres.ExtensionDir(root, "timescaledb", "18", "2.31.0-build1")
	custom := postgres.CustomDir(root, "18")
	for _, file := range []string{
		filepath.Join(build, "bin", exe),
		filepath.Join(build, "lib", "postgresql", "plpgsql"+module),
		filepath.Join(build, "share", "postgresql", "extension", "plpgsql.control"),
		filepath.Join(postgres.DataDir(root, "18"), "PG_VERSION"),
		filepath.Join(timescale, "lib", "timescaledb"+module),
		filepath.Join(timescale, "extension", "timescaledb.control"),
		filepath.Join(custom, "lib", "hello"+module),
		filepath.Join(custom, "extension", "hello.control"),
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	d := &daemon{root: root}
	spec, warnings, err := d.postgresSpec(config.Default(), id)
	if err != nil || len(warnings) > 0 {
		t.Fatal(err, warnings)
	}
	args := strings.Join(spec.Args, " ")
	for _, want := range []string{"shared_preload_libraries=timescaledb", "timescaledb.telemetry_level=off"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q lack %s", args, want)
		}
	}
	for _, linked := range []string{
		filepath.Join(build, "lib", "postgresql", "timescaledb"+module),
		filepath.Join(build, "lib", "postgresql", "hello"+module),
		filepath.Join(build, "share", "postgresql", "extension", "timescaledb.control"),
		filepath.Join(build, "share", "postgresql", "extension", "hello.control"),
	} {
		if _, err := os.Stat(linked); err != nil {
			t.Errorf("%s not linked: %v", linked, err)
		}
	}

	// Gone from the custom folder, gone from the server; without TimescaleDB, nothing is preloaded.
	if err := os.Remove(filepath.Join(custom, "lib", "hello"+module)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(timescale)); err != nil {
		t.Fatal(err)
	}
	spec, _, err = d.postgresSpec(config.Default(), id)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(spec.Args, func(a string) bool { return strings.Contains(a, "timescaledb") }) {
		t.Errorf("still preloads TimescaleDB: %q", spec.Args)
	}
	if _, err := os.Lstat(filepath.Join(build, "lib", "postgresql", "hello"+module)); err == nil {
		t.Error("removed custom extension is still linked")
	}
}
