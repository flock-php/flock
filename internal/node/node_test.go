package node

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"flock/internal/binaries"
	"flock/internal/binaries/binariestest"
)

// nodePackage is a Node.js archive as nodejs.org publishes it, in one top-level directory.
func nodePackage(t *testing.T, version string) []byte {
	top := "node-v" + version
	if runtime.GOOS == "windows" {
		return binariestest.Zip(t, map[string]string{top + "/node.exe": version, top + "/npm.cmd": "npm"})
	}
	return binariestest.Zip(t, map[string]string{top + "/bin/node": version, top + "/lib/node_modules/npm/bin/npm-cli.js": "npm"})
}

func publishedNode(t *testing.T) *Installer {
	t.Helper()
	if binaries.Platform() == "" {
		t.Skip("no packages for this platform")
	}
	srv := binariestest.New(t)
	srv.Publish(Package, "22.11.0", 1, nodePackage(t, "22.11.0"), nil)
	srv.Publish(Package, "20.18.0", 1, nodePackage(t, "20.18.0"), nil)
	srv.Publish(Package, "14.21.3", 1, nodePackage(t, "14.21.3"), nil)
	root := t.TempDir()
	return &Installer{Root: root, Catalog: srv.Catalog(filepath.Join(root, "cache"))}
}

func TestInstallInstallsTheNewestOfTheMajor(t *testing.T) {
	inst := publishedNode(t)
	root := inst.Root

	version, err := inst.Install(context.Background(), "22")
	if err != nil || version != "22.11.0" {
		t.Fatalf("want 22.11.0, got %q, %v", version, err)
	}
	binDir, resolved, err := Resolve(root, "22")
	if err != nil || resolved != "22.11.0" {
		t.Fatalf("resolve: %q, %v", resolved, err)
	}
	name := "node"
	if runtime.GOOS == "windows" {
		name = "node.exe"
	}
	if data, err := os.ReadFile(filepath.Join(binDir, name)); err != nil || string(data) != "22.11.0" {
		t.Fatalf("node: %q %v", data, err)
	}

	// A pinned patch gets the newest release of its major, and installing again is a no-op.
	for _, spec := range []string{"22.9.0", "22.11", "v22"} {
		if again, err := inst.Install(context.Background(), spec); err != nil || again != "22.11.0" {
			t.Fatalf("%s: %q, %v", spec, again, err)
		}
	}
	for _, spec := range []string{"99", "lts/*", ""} {
		if _, err := inst.Install(context.Background(), spec); err == nil {
			t.Fatalf("%q must not install", spec)
		}
	}
	if versions, _ := Installed(root); strings.Join(versions, ",") != "22.11.0" {
		t.Fatalf("installed %v", versions)
	}
}

func TestLinesAreThePublishedMajors(t *testing.T) {
	inst := publishedNode(t)
	manifest, err := inst.Catalog.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lines := Lines(manifest)
	// 14 is older than MinimumMajor.
	if len(lines) != 2 || lines[0] != (Line{Major: "22", Latest: "22.11.0"}) || lines[1].Major != "20" {
		t.Fatalf("lines %+v", lines)
	}
	if len(Lines(nil)) != 0 {
		t.Fatal("no manifest, no lines")
	}
}

func TestNewestMatch(t *testing.T) {
	versions := []string{"22.9.0", "23.1.0", "22.11.0", "20.18.0"}
	cases := map[string]string{"22": "22.11.0", "22.9": "22.9.0", "20.18.0": "20.18.0", "23": "23.1.0"}

	for spec, want := range cases {
		if got, ok := newestMatch(versions, spec); !ok || got != want {
			t.Errorf("%s: want %s, got %s", spec, want, got)
		}
	}
	if _, ok := newestMatch(versions, "2"); ok {
		t.Error(`"2" must not match 22.x`)
	}
}

func TestResolvePicksNewestInstalled(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"22.9.0", "22.11.0", "20.18.0", ".tmp-22.12.0-x"} {
		if err := os.MkdirAll(filepath.Join(Dir(root), v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	installed, _ := Installed(root)
	if strings.Join(installed, ",") != "22.11.0,22.9.0,20.18.0" {
		t.Fatalf("unexpected installed list: %v", installed)
	}

	if _, v, err := Resolve(root, "22"); err != nil || v != "22.11.0" {
		t.Fatalf("want 22.11.0, got %q, %v", v, err)
	}
	// A pinned patch that isn't installed resolves to the newest of its major.
	if _, v, err := Resolve(root, "22.10.3"); err != nil || v != "22.11.0" {
		t.Fatalf("pinned patch: %q, %v", v, err)
	}
	if _, _, err := Resolve(root, "18"); err == nil {
		t.Fatal("want error when nothing matches")
	}
}

func TestDetectSpecWalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "app", "resources", "js")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := DetectSpec(deep); got != "" {
		t.Fatalf("want empty, got %q", got)
	}

	_ = os.WriteFile(filepath.Join(root, "app", ".node-version"), []byte("v20.18.0\n"), 0o644)
	if got := DetectSpec(deep); got != "20.18.0" {
		t.Fatalf("want 20.18.0, got %q", got)
	}

	_ = os.WriteFile(filepath.Join(root, "app", "resources", ".nvmrc"), []byte(" 22 \n"), 0o644)
	if got := DetectSpec(deep); got != "22" {
		t.Fatalf("want 22, got %q", got)
	}

	_ = os.WriteFile(filepath.Join(deep, ".nvmrc"), []byte("lts/*\n"), 0o644)
	if got := DetectSpec(deep); got != "" {
		t.Fatalf("aliases must map to empty, got %q", got)
	}
}
