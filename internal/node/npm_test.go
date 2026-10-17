package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSatisfies(t *testing.T) {
	for _, c := range []struct {
		version, rng string
		want         bool
	}{
		{"22.11.0", "^20.17.0 || >=22.9.0", true},
		{"22.8.0", "^20.17.0 || >=22.9.0", false},
		{"20.18.1", "^20.17.0 || >=22.9.0", true},
		{"21.0.0", "^20.17.0 || >=22.9.0", false},
		{"18.20.8", ">= 18.17.0", true},
		{"18.16.0", ">= 18.17.0", false},
		{"16.20.2", "^14.17.0 || ^16.13.0 || >=18.0.0", true},
		{"20.0.0", "18 - 20", true},
		{"21.0.0", "18 - 20", false},
		{"20.5.1", "20.x", true},
		{"19.0.0", ">=16 <20", true},
		{"20.0.0", ">=16 <20", false},
		{"24.0.0", "*", true},
		{"20.1.0", "~20.1.0", true},
		{"20.2.0", "~20.1.0", false},
	} {
		if got := Satisfies(c.version, c.rng); got != c.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", c.version, c.rng, got, c.want)
		}
	}
}

func TestLatestNpmForPicksNewestCompatibleStable(t *testing.T) {
	var p npmPackument
	if err := json.Unmarshal([]byte(`{"versions": {
		"10.9.2":       {"engines": {"node": "^18.17.0 || >=20.5.0"}},
		"11.20.0":      {"engines": {"node": "^20.17.0 || >=22.9.0"}},
		"12.2.0":       {"engines": {"node": "^22.14.0 || >=24.0.0"}},
		"13.0.0-pre.1": {"engines": {"node": ">=24"}},
		"12.3.0":       {"engines": {"node": ">=24"}, "deprecated": "broken release"}
	}}`), &p); err != nil {
		t.Fatal(err)
	}
	for node, want := range map[string]string{"26.1.0": "12.2.0", "22.21.0": "12.2.0", "22.9.0": "11.20.0", "18.20.8": "10.9.2", "16.20.2": ""} {
		if got := p.latestNpmFor(node); got != want {
			t.Errorf("Node %s: npm %q, want %q", node, got, want)
		}
	}
}

// writePackage fakes an installed package (just its package.json).
func writePackage(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNpmPrefersTheUpdatedOne(t *testing.T) {
	root := t.TempDir()
	writePackage(t, filepath.Join(modulesDir(VersionDir(root, "26.1.0")), "npm"), "11.20.0")
	if v, _ := Npm(root, "26.1.0"); v != "11.20.0" {
		t.Fatalf("bundled npm: %q", v)
	}
	writePackage(t, filepath.Join(modulesDir(GlobalDir(root, "26")), "npm"), "12.2.0")
	if v, cli := Npm(root, "26.1.0"); v != "12.2.0" || filepath.Base(cli) != "npm-cli.js" {
		t.Fatalf("updated npm: %q %q", v, cli)
	}
}

func TestLinkCurrentFollowsTheDefault(t *testing.T) {
	root := t.TempDir()
	for _, major := range []string{"22", "24"} {
		if err := LinkCurrent(root, major); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(GlobalDir(root, major), "marker-"+major)
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(CurrentGlobalDir(root), "marker-"+major)); err != nil {
			t.Fatalf("current doesn't lead to %s: %v", major, err)
		}
	}
	// Switching the link must not delete the previous major's packages.
	if _, err := os.Stat(filepath.Join(GlobalDir(root, "22"), "marker-22")); err != nil {
		t.Fatal(err)
	}
}
