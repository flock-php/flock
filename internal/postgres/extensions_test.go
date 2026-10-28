package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flock/internal/binaries/binariestest"
)

// extPackage is a fake extension package: a module, a control file and a dependency.
func extPackage(t *testing.T, name string) []byte {
	return binariestest.Zip(t, map[string]string{
		"lib/" + name + ".so":            name,
		"extension/" + name + ".control": name,
		"deps/lib" + name + "dep.so":     "",
		"LICENSES/" + name + ".txt":      "",
	})
}

func extension(t *testing.T, m *Manager, major, name string) ExtensionState {
	t.Helper()
	state, err := m.Extensions(major)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range state.Extensions {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("extension %s missing", name)
	return ExtensionState{}
}

func linkedIn(m *Manager, id, file string) bool {
	_, err := os.Lstat(filepath.Join(m.Installer.Dir(id), file))
	return err == nil
}

func TestExtensionsPerMajor(t *testing.T) {
	m, svc, srv := newTestManager(t)
	ctx := context.Background()
	srv.Publish(Package, "18.6", 1, pgPackage(t), nil)
	srv.Publish(Package, "17.6", 1, pgPackage(t), nil)
	srv.Publish(ExtensionPackage("pgvector", "18"), "0.8.7", 1, extPackage(t, "vector"), nil)
	srv.Publish(ExtensionPackage("pgvector", "17"), "0.8.7", 1, extPackage(t, "vector"), nil)
	srv.Publish(ExtensionPackage("timescaledb", "18"), "2.31.0", 1, extPackage(t, "timescaledb"), nil)
	// TimescaleDB stopped at 2.30 for 17: it stays there.
	srv.Publish(ExtensionPackage("timescaledb", "17"), "2.30.2", 1, extPackage(t, "timescaledb"), nil)

	// A new major gets the extensions it asks for, before its server first starts.
	if err := m.Install(ctx, "18", "pgvector", "timescaledb"); err != nil {
		t.Fatal(err)
	}
	if got := major(t, m.Versions(), "18").Extensions; !slices.Equal(got, []string{"pgvector", "timescaledb"}) {
		t.Fatalf("18 has %v", got)
	}
	if !linkedIn(m, svc.current(), "lib/postgresql/vector.so") || !linkedIn(m, svc.current(), "share/postgresql/extension/timescaledb.control") {
		t.Fatal("extensions are not linked into the active server")
	}
	if linkedIn(m, svc.current(), "lib/postgresql/libvectordep.so") {
		t.Fatal("a dependency was linked into pkglibdir")
	}
	if got := PreloadLibraries(m.root(), "18"); !slices.Equal(got, []string{"timescaledb"}) {
		t.Fatalf("preload %v", got)
	}

	// Picked per major: 17 has none of them.
	if err := m.Install(ctx, "17"); err != nil {
		t.Fatal(err)
	}
	if got := major(t, m.Versions(), "17").Extensions; len(got) != 0 {
		t.Fatalf("17 has %v", got)
	}
	if err := m.InstallExtension(ctx, "17", "timescaledb"); err != nil {
		t.Fatal(err)
	}
	if e := extension(t, m, "17", "timescaledb"); e.Installed != "2.30.2" || !e.Discontinued || e.Update {
		t.Fatalf("17's TimescaleDB: %+v", e)
	}
	if e := extension(t, m, "18", "timescaledb"); e.Installed != "2.31.0" || e.Discontinued {
		t.Fatalf("18's TimescaleDB: %+v", e)
	}
	if e := extension(t, m, "17", "postgis"); e.Supported {
		t.Fatalf("PostGIS has no build but is supported: %+v", e)
	}
	if err := m.InstallExtension(ctx, "17", "postgis"); err == nil {
		t.Fatal("installed an extension without a build")
	}

	// An update replaces the build and keeps it linked.
	srv.Publish(ExtensionPackage("pgvector", "18"), "0.8.8", 1, extPackage(t, "vector"), nil)
	if _, err := m.Catalog.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if e := extension(t, m, "18", "pgvector"); !e.Update || e.Latest != "0.8.8" {
		t.Fatalf("no update offered: %+v", e)
	}
	if err := m.UpdateExtension(ctx, "18", "pgvector"); err != nil {
		t.Fatal(err)
	}
	if got := installedExtensionIDs(m.root(), "pgvector", "18"); !slices.Equal(got, []string{"0.8.8-build1"}) {
		t.Fatalf("pgvector builds for 18: %v", got)
	}
	if !linkedIn(m, svc.current(), "lib/postgresql/vector.so") {
		t.Fatal("pgvector is not linked after its update")
	}

	// Uninstalling removes it from the server.
	if err := m.UninstallExtension(ctx, "18", "pgvector"); err != nil {
		t.Fatal(err)
	}
	if linkedIn(m, svc.current(), "lib/postgresql/vector.so") {
		t.Fatal("uninstalled pgvector is still linked")
	}
	if err := m.UninstallExtension(ctx, "18", "pgvector"); err == nil {
		t.Fatal("uninstalled an extension twice")
	}
	if err := m.InstallExtension(ctx, "18", "nope"); err == nil {
		t.Fatal("installed an unknown extension")
	}

	// pgRouting needs PostGIS: it comes along, and can't be removed from under it.
	srv.Publish(ExtensionPackage("postgis", "18"), "3.6.4", 1, extPackage(t, "postgis-3"), nil)
	srv.Publish(ExtensionPackage("pgrouting", "18"), "4.0.2", 1, extPackage(t, "libpgrouting-4.0"), nil)
	if _, err := m.Catalog.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallExtension(ctx, "18", "pgrouting"); err != nil {
		t.Fatal(err)
	}
	if got := InstalledExtensions(m.root(), "18"); !slices.Equal(got, []string{"postgis", "pgrouting", "timescaledb"}) {
		t.Fatalf("18 has %v", got)
	}
	if err := m.UninstallExtension(ctx, "18", "postgis"); !errors.Is(err, ErrExtensionInUse) {
		t.Fatalf("removed PostGIS from under pgRouting: %v", err)
	}
	if err := m.UninstallExtension(ctx, "18", "pgrouting"); err != nil {
		t.Fatal(err)
	}
	if err := m.UninstallExtension(ctx, "18", "postgis"); err != nil {
		t.Fatal(err)
	}

	// Uninstalling a major takes its extensions, but not its custom folder.
	custom, err := EnsureCustomDir(m.root(), "17")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(ctx, "17", false); err != nil {
		t.Fatal(err)
	}
	if InstalledExtension(m.root(), "timescaledb", "17") != "" {
		t.Fatal("17's TimescaleDB survived uninstalling 17")
	}
	if _, err := os.Stat(filepath.Join(custom, "README.txt")); err != nil {
		t.Fatalf("the custom folder went with 17: %v", err)
	}
}

func TestLinkKeepsServerFilesAndFollowsCustomFolder(t *testing.T) {
	m, svc, srv := newTestManager(t)
	ctx := context.Background()
	srv.Publish(Package, "18.6", 1, pgPackage(t), nil)
	if err := m.Install(ctx, "18"); err != nil {
		t.Fatal(err)
	}
	id := svc.current()
	custom, err := EnsureCustomDir(m.root(), "18")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"lib/hello.so": "hello", "extension/hello.control": "", "lib/plpgsql.so": "mine"} {
		if err := os.WriteFile(filepath.Join(custom, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	changed, warnings, err := Link(m.root(), id)
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	// The server's own plpgsql wins, and the conflict is reported.
	if len(warnings) != 1 {
		t.Fatalf("warnings %q", warnings)
	}
	if data, _ := os.ReadFile(filepath.Join(m.Installer.Dir(id), "lib/postgresql/plpgsql.so")); string(data) != "" {
		t.Fatal("the server's plpgsql was replaced")
	}
	if !linkedIn(m, id, "lib/postgresql/hello.so") {
		t.Fatal("hello.so is not linked")
	}
	if changed, _, _ := Link(m.root(), id); changed {
		t.Fatal("a second Link changed something")
	}

	// A new server build of the major gets the same files.
	srv.Publish(Package, "18.7", 1, pgPackage(t), nil)
	if err := m.Update(ctx, "18"); err != nil {
		t.Fatal(err)
	}
	if svc.current() == id || !linkedIn(m, svc.current(), "lib/postgresql/hello.so") {
		t.Fatal("custom extension lost on update")
	}
}

func TestLibraryDirs(t *testing.T) {
	root := t.TempDir()
	postgis := ExtensionDir(root, "postgis", "18", "3.6.4-build1")
	for _, dir := range []string{filepath.Join(postgis, "extension"), filepath.Join(postgis, "deps"), filepath.Join(CustomDir(root, "18"), "deps")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// An empty custom deps/ isn't listed: creating the folder must not restart the server.
	if got := LibraryDirs(root, "18"); !slices.Equal(got, []string{filepath.Join(postgis, "deps")}) {
		t.Fatalf("got %v", got)
	}
	if err := os.WriteFile(filepath.Join(CustomDir(root, "18"), "deps", "libfoo.so"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LibraryDirs(root, "18"); len(got) != 2 || got[1] != filepath.Join(CustomDir(root, "18"), "deps") {
		t.Fatalf("got %v", got)
	}
	if got := LibraryDirs(root, "17"); len(got) != 0 {
		t.Fatalf("17 got %v", got)
	}
}

// Extension operations queue behind the one that runs instead of failing; the queued one is
// "waiting", and a second click on it is refused.
func TestExtensionOperationsQueue(t *testing.T) {
	m, _, srv := newTestManager(t)
	ctx := context.Background()
	srv.Publish(Package, "18.6", 1, pgPackage(t), nil)
	srv.Publish(ExtensionPackage("pgvector", "18"), "0.8.7", 1, extPackage(t, "vector"), nil)
	srv.Publish(ExtensionPackage("postgis", "18"), "3.6.4", 1, extPackage(t, "postgis-3"), nil)
	if err := m.Install(ctx, "18"); err != nil {
		t.Fatal(err)
	}

	m.op <- struct{}{} // an operation runs
	errs := make(chan error, 2)
	go func() { errs <- m.InstallExtension(ctx, "18", "pgvector") }()
	go func() { errs <- m.InstallExtension(ctx, "18", "postgis") }()
	deadline := time.Now().Add(5 * time.Second)
	for extension(t, m, "18", "pgvector").Busy != "waiting" || extension(t, m, "18", "postgis").Busy != "waiting" {
		if time.Now().After(deadline) {
			t.Fatal("installs are not waiting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.InstallExtension(ctx, "18", "pgvector"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second install of a waiting extension: %v", err)
	}
	if err := m.Install(ctx, "17"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a version operation must not wait: %v", err)
	}

	<-m.op // it ends: both queued installs run, one after the other
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := InstalledExtensions(m.root(), "18"); !slices.Equal(got, []string{"postgis", "pgvector"}) {
		t.Fatalf("installed %v", got)
	}
	if e := extension(t, m, "18", "pgvector"); e.Busy != "" {
		t.Fatalf("still busy: %q", e.Busy)
	}

	// A waiting operation gives up with its context.
	m.op <- struct{}{}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.UninstallExtension(cancelled, "18", "pgvector"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	<-m.op
	if e := extension(t, m, "18", "pgvector"); e.Busy != "" {
		t.Fatalf("cancelled operation still marked: %q", e.Busy)
	}
}
