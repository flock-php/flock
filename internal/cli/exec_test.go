package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/config"
	"flock/internal/php"
)

// fakePHP creates an "installed" PHP version so php.Installed reports it.
func fakePHP(t *testing.T, root, minor string) {
	t.Helper()
	bin := php.Binary(root, minor)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveToolUsesTheSitePHPVersion(t *testing.T) {
	root := t.TempDir()
	fakePHP(t, root, "8.4")
	fakePHP(t, root, "8.5")

	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "legacy", Path: "/code/legacy", PHP: "8.4"})
	s := &state{root: root, cfg: cfg}

	path, argv, env, err := resolveTool(s, "/code/legacy/app", "php", []string{"artisan"})
	if err != nil {
		t.Fatal(err)
	}
	if path != php.Binary(root, "8.4") || strings.Join(argv, " ") != php.Binary(root, "8.4")+" artisan" || env[0] != "PHPRC="+php.IniPath(root, "8.4") {
		t.Fatalf("got %s %v %v", path, argv, env)
	}

	path, _, _, _ = resolveTool(s, "/elsewhere", "php", nil)
	if path != php.Binary(root, "8.5") {
		t.Fatalf("outside sites the default must be used, got %s", path)
	}
}

func TestResolveToolPassesTheAbsoluteBinaryAsArgv0(t *testing.T) {
	root := t.TempDir()
	fakePHP(t, root, "8.5")
	s := &state{root: root, cfg: config.Default()}

	path, argv, _, err := resolveTool(s, "/x", "php", nil)
	if err != nil || argv[0] != path || !filepath.IsAbs(argv[0]) {
		t.Fatalf("PHP_BINARY would not point at Flock's PHP: argv=%v err=%v", argv, err)
	}
}

func TestResolveToolRunsComposerThroughPHP(t *testing.T) {
	root := t.TempDir()
	fakePHP(t, root, "8.5")
	s := &state{root: root, cfg: config.Default()}

	_, argv, _, err := resolveTool(s, "/x", "composer", []string{"install"})
	if err != nil || argv[1] != php.ComposerPhar(root) || argv[2] != "install" {
		t.Fatalf("got %v, %v", argv, err)
	}
}

func TestResolveToolExplainsMissingVersions(t *testing.T) {
	s := &state{root: t.TempDir(), cfg: config.Default()}

	if _, _, _, err := resolveTool(s, "/x", "php", nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("got %v", err)
	}
	if _, _, _, err := resolveTool(s, "/x", "node", nil); err == nil {
		t.Fatal("expected error for missing node")
	}
}
