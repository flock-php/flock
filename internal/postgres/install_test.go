package postgres

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/binaries"
	"flock/internal/binaries/binariestest"
)

func TestInstallerInstallsAPublishedBuild(t *testing.T) {
	if binaries.Platform() == "" {
		t.Skip("no packages for this platform")
	}
	srv := binariestest.New(t)
	root := t.TempDir()
	srv.Publish(Package, "18.6", 2, binariestest.Zip(t, map[string]string{
		"bin/" + exe("postgres"): "server",
		"bin/" + exe("psql"):     "client",
		"lib/libpq.5.dylib":      "lib",
		"share/postgres.bki":     "share",
	}), nil)
	srv.Publish(Package, "17.7", 1, binariestest.Zip(t, map[string]string{"README": "no binaries"}), nil)
	manifest, err := srv.Catalog(filepath.Join(root, "cache")).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	published := offers(manifest, binaries.Platform())

	inst := NewInstaller(root)
	id, err := inst.Install(context.Background(), published["18"])
	if err != nil || id != "18.6-build2" || !inst.Installed(id) {
		t.Fatalf("installed %q: %v", id, err)
	}
	if data, _ := os.ReadFile(filepath.Join(inst.Dir(id), "share", "postgres.bki")); string(data) != "share" {
		t.Fatal("share/ wasn't installed")
	}
	// Installing it again does nothing.
	if again, err := inst.Install(context.Background(), published["18"]); err != nil || again != id {
		t.Fatalf("second install: %q %v", again, err)
	}
	// A package without the server is refused and leaves nothing behind.
	if _, err := inst.Install(context.Background(), published["17"]); err == nil || !strings.Contains(err.Error(), "bin/") {
		t.Fatalf("a package without postgres: %v", err)
	}
	if ids, _ := InstalledIDs(root); len(ids) != 1 {
		t.Fatalf("installed: %v", ids)
	}
}
