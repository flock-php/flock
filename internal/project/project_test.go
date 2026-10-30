package project

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestVersionsFollowIsolationThenProjectThenDefault(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DefaultPHP, cfg.DefaultNode = "8.5", "22"
	site := &config.Site{Path: dir}

	if v := PHPFor(cfg, site, dir); v.Version != "8.5" || v.Source != Default || v.Pinned != "" {
		t.Fatalf("nothing pinned: %+v", v)
	}

	write(t, filepath.Join(dir, ".nvmrc"), "v20\n")
	if v := NodeFor(cfg, site, filepath.Join(dir, "resources", "js")); v.Version != "20" || v.Source != Project || filepath.Base(v.File) != ".nvmrc" {
		t.Fatalf(".nvmrc, from a subdirectory: %+v", v)
	}

	write(t, filepath.Join(dir, RC), "# team versions\nphp = 8.4\nnode=24\n")
	if v := PHPFor(cfg, site, dir); v.Version != "8.4" || v.Source != Project {
		t.Fatalf(".flockrc php: %+v", v)
	}
	if v := NodeFor(cfg, site, dir); v.Version != "24" || filepath.Base(v.File) != RC || v.Shadowed != ".nvmrc: 20" {
		t.Fatalf(".flockrc wins over .nvmrc and says so: %+v", v)
	}

	site.PHP, site.Node = "8.3", "26"
	if v := PHPFor(cfg, site, dir); v.Version != "8.3" || v.Source != Isolated || v.Pinned != "8.4" {
		t.Fatalf("isolation wins, the pin stays visible: %+v", v)
	}
	if v := NodeFor(cfg, site, dir); v.Version != "26" || v.Source != Isolated || v.Pinned != "24" {
		t.Fatalf("node isolation: %+v", v)
	}

	// Outside any site, the project files still count.
	if v := NodeFor(cfg, nil, dir); v.Version != "24" {
		t.Fatalf("no site: %+v", v)
	}
}

func TestPinNodeWritesTheFileTheProjectUses(t *testing.T) {
	dir := t.TempDir()
	file, err := Pin(dir, Node, "22")
	if err != nil || filepath.Base(file) != ".nvmrc" || read(t, file) != "22\n" {
		t.Fatalf("new project: %s %v", file, err)
	}

	other := t.TempDir()
	write(t, filepath.Join(other, ".node-version"), "18.20.0\n")
	if file, _ := Pin(other, Node, "22"); filepath.Base(file) != ".node-version" || read(t, file) != "22\n" {
		t.Fatalf(".node-version project: %s", file)
	}

	rc := t.TempDir()
	write(t, filepath.Join(rc, RC), "php=8.4\nnode=20\n")
	if file, _ := Pin(rc, Node, "22"); filepath.Base(file) != RC || read(t, file) != "php=8.4\nnode=22\n" {
		t.Fatalf(".flockrc with node=: %s %q", file, read(t, filepath.Join(rc, RC)))
	}

	if _, err := Pin(dir, Node, "22.1"); err == nil {
		t.Fatal("Node pins are majors")
	}
}

func TestPinAndUnpinPHPKeepOtherSettings(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, RC)
	write(t, rc, "# keep me\nnode=22\n")

	if _, err := Pin(dir, PHP, "8.4"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, rc); got != "# keep me\nnode=22\nphp=8.4\n" {
		t.Fatalf("after pin: %q", got)
	}
	if _, err := Pin(dir, PHP, "8.3"); err != nil || read(t, rc) != "# keep me\nnode=22\nphp=8.3\n" {
		t.Fatalf("repin: %q %v", read(t, rc), err)
	}

	changed, err := Unpin(dir, PHP)
	if err != nil || !slices.Equal(changed, []string{rc}) || read(t, rc) != "# keep me\nnode=22\n" {
		t.Fatalf("unpin php: %v %v %q", changed, err, read(t, rc))
	}

	write(t, filepath.Join(dir, ".nvmrc"), "22\n")
	if changed, _ := Unpin(dir, Node); len(changed) != 2 {
		t.Fatalf("unpin node changes .flockrc and .nvmrc: %v", changed)
	}
	for _, name := range []string{RC, ".nvmrc"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should be gone (nothing left in it): %v", name, err)
		}
	}
	if changed, _ := Unpin(dir, Node); len(changed) != 0 {
		t.Fatalf("nothing left to unpin: %v", changed)
	}
}

func TestPinningInAWorktreeIsRefusedWhileItsSiteSetsTheVersionInFlock(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", "www", "index.php"), "<?php\n")
	cfg := &config.Config{DefaultPHP: "8.5"}
	// It copies acme's settings, which pin nothing over.
	site := &config.Site{Name: "feature.acme", Path: dir, PHP: "8.4", Node: "22", Root: "src/www", Parent: "acme"}

	for tool, call := range map[string]func() error{
		PHP:  func() error { _, _, err := PinSite(cfg, site, PHP, "8.5"); return err },
		Node: func() error { _, _, err := PinSite(cfg, site, Node, "24"); return err },
		Root: func() error { _, _, err := PinSiteRoot(site, "src/www"); return err },
	} {
		if err := call(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "in acme first") {
			t.Errorf("pin %s: %v", tool, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, RC)); !os.IsNotExist(err) {
		t.Fatalf("a refused pin wrote %s", RC)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nvmrc")); !os.IsNotExist(err) {
		t.Fatal("a refused pin wrote .nvmrc")
	}

	// Without a setting on the site, the branch pins its own version.
	site.PHP = ""
	if file, lifted, err := PinSite(cfg, site, PHP, "8.5"); err != nil || lifted || read(t, file) != "php=8.5\n" {
		t.Fatalf("pin in a worktree of a site without isolation: %q %v %v", file, lifted, err)
	}
}
