package node

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"flock/internal/binaries"
)

func TestUninstallAndRemoveOlder(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"22.20.0", "22.21.0", "20.19.5"} {
		if err := os.MkdirAll(filepath.Join(Dir(root), v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	RemoveOlder(root, "22.21.0")
	if got, _ := Installed(root); !slices.Equal(got, []string{"22.21.0", "20.19.5"}) {
		t.Fatalf("after RemoveOlder: %v", got)
	}
	if err := Uninstall(root, "22"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Installed(root); !slices.Equal(got, []string{"20.19.5"}) {
		t.Fatalf("after Uninstall: %v", got)
	}
}

func TestLatestLTSIsTheNewestMajorWhoseNewestReleaseIsLTS(t *testing.T) {
	platform := binaries.Platform()
	if platform == "" {
		t.Skip("no packages for this platform")
	}
	release := func(lts bool) map[string]binaries.Entry {
		return map[string]binaries.Entry{platform: {Build: 1, URL: "u", SHA256: "s", LTS: lts}}
	}
	m := &binaries.Manifest{Packages: map[string]map[string]map[string]binaries.Entry{Package: {
		"14.21.3": release(true), // older than MinimumMajor
		"22.23.3": release(true),
		"24.22.0": release(true),
		"25.9.0":  release(false),
		"26.1.0":  release(false), // Current: not LTS yet
	}}}
	if got := LatestLTS(m); got != "24" {
		t.Errorf("got %q, want 24", got)
	}
	// 26's first LTS release makes it the newest LTS line.
	m.Packages[Package]["26.2.0"] = release(true)
	if got := LatestLTS(m); got != "26" {
		t.Errorf("after 26 went LTS: got %q, want 26", got)
	}
	if got := LatestLTS(nil); got != "" {
		t.Errorf("no manifest: got %q", got)
	}
}
