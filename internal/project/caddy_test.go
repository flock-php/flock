package project

import (
	"os"
	"path/filepath"
	"testing"

	"flock/internal/config"
)

func writeSettings(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCaddyFileIsOnlyWhatTheSettingsName(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	site := config.Site{Name: "app", Path: dir}

	// A Caddyfile in the project is the project's own, e.g. for production: Flock leaves it alone.
	writeSettings(t, filepath.Join(dir, "Caddyfile"), "example.com {\n\tphp_server\n}\n")
	if file, _, err := CaddyFile(cfg, site); err != nil || file != "" {
		t.Fatalf("without settings: got %q, %v", file, err)
	}

	writeSettings(t, filepath.Join(dir, ConfigFile), "caddyfile: ops/local.caddy\nworktrees:\n  build: false\n")
	file, from, err := CaddyFile(cfg, site)
	if err != nil || file != filepath.Join(dir, "ops", "local.caddy") || from != filepath.Join(dir, ConfigFile) {
		t.Fatalf("flock.yml: got %q from %q, %v", file, from, err)
	}

	// flock.local.yml replaces flock.yml; an absolute path is used as it is.
	other := filepath.Join(t.TempDir(), "mine.caddy")
	writeSettings(t, filepath.Join(dir, LocalFile), "caddyfile: "+other+"\n")
	if file, from, err = CaddyFile(cfg, site); err != nil || file != other || from != filepath.Join(dir, LocalFile) {
		t.Fatalf("flock.local.yml: got %q from %q, %v", file, from, err)
	}

	// An empty value turns the committed one off.
	writeSettings(t, filepath.Join(dir, LocalFile), "caddyfile: \"\"\n")
	if file, _, err = CaddyFile(cfg, site); err != nil || file != "" {
		t.Fatalf("empty in flock.local.yml: got %q, %v", file, err)
	}

	writeSettings(t, filepath.Join(dir, LocalFile), "caddyfile: [nope\n")
	if _, _, err = CaddyFile(cfg, site); err == nil {
		t.Fatal("broken flock.local.yml: no error")
	}
}

func TestCaddyFileOfAWorktreeFollowsItsSettingsFiles(t *testing.T) {
	parent, wt := t.TempDir(), t.TempDir()
	cfg := &config.Config{Sites: []config.Site{{Name: "shop", Path: parent}}}
	site := config.Site{Name: "feature.shop", Path: wt, Parent: "shop"}

	// The site's flock.yml isn't the worktree's: the branch brings its own.
	writeSettings(t, filepath.Join(parent, ConfigFile), "caddyfile: site.caddy\n")
	if file, _, err := CaddyFile(cfg, site); err != nil || file != "" {
		t.Fatalf("site's flock.yml: got %q, %v", file, err)
	}

	writeSettings(t, filepath.Join(wt, ConfigFile), "caddyfile: branch.caddy\n")
	if file, _, _ := CaddyFile(cfg, site); file != filepath.Join(wt, "branch.caddy") {
		t.Fatalf("worktree's flock.yml: got %q", file)
	}

	// The site's flock.local.yml applies to its worktrees, relative to the site.
	writeSettings(t, filepath.Join(parent, LocalFile), "caddyfile: mine.caddy\n")
	if file, _, _ := CaddyFile(cfg, site); file != filepath.Join(parent, "mine.caddy") {
		t.Fatalf("site's flock.local.yml: got %q", file)
	}

	writeSettings(t, filepath.Join(wt, LocalFile), "caddyfile: here.caddy\n")
	if file, _, _ := CaddyFile(cfg, site); file != filepath.Join(wt, "here.caddy") {
		t.Fatalf("worktree's flock.local.yml: got %q", file)
	}
}
