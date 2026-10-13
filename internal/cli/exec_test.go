package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/valkey"
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

func TestNodeGetsTheSiteCertificateForViteWhenSecured(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "node", "22.1.0", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Upsert(config.Site{Name: "shop", Path: "/code/shop", Secured: true})
	cfg.Upsert(config.Site{Name: "blog", Path: "/code/blog"})
	s := &state{root: root, cfg: cfg}

	certPath, keyPath := certs.SitePaths(filepath.Join(root, "certs"), "shop.test")
	for _, p := range []string{certPath, keyPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VITE_DEV_SERVER_KEY", "")

	_, _, env, err := resolveTool(s, "/code/shop", "npm", []string{"run", "dev"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "VITE_DEV_SERVER_KEY="+keyPath) || !strings.Contains(joined, "VITE_DEV_SERVER_CERT="+certPath) {
		t.Fatalf("vite TLS env missing: %v", env)
	}

	_, _, env, _ = resolveTool(s, "/code/blog", "npm", nil)
	if strings.Contains(strings.Join(env, "\n"), "VITE_DEV_SERVER") {
		t.Fatalf("unsecured site must not get vite TLS env: %v", env)
	}
}

func TestPostgresToolsDefaultToFlocksServer(t *testing.T) {
	root := t.TempDir()
	psql := filepath.Join(postgres.ClientDir(root, "18.6"), "bin", "psql")
	if err := os.MkdirAll(filepath.Dir(psql), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(psql, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &state{root: root, cfg: config.Default()}
	t.Setenv("PGHOST", "")
	t.Setenv("PGUSER", "me")

	path, argv, env, err := resolveTool(s, "/x", "psql", []string{"watchtower"})
	if err != nil || path != psql || argv[1] != "watchtower" {
		t.Fatalf("got %s %v %v", path, argv, err)
	}
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, "PGHOST=127.0.0.1") || !strings.Contains(joined, "PGPORT=5432") || strings.Contains(joined, "PGUSER") {
		t.Fatalf("PGHOST/PGPORT must default, an explicit PGUSER must win: %v", env)
	}
}

func TestRedisCLIUsesTheConfiguredPort(t *testing.T) {
	root := t.TempDir()
	cli := valkey.CLI(root, "9.1.2")
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{cli, valkey.Server(root, "9.1.2")} {
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Ports.Redis = 6380
	s := &state{root: root, cfg: cfg}

	_, argv, _, err := resolveTool(s, "/x", "redis-cli", []string{"ping"})
	if err != nil || strings.Join(argv[1:], " ") != "-p 6380 ping" {
		t.Fatalf("got %v %v", argv, err)
	}
}

func TestShimToolRecognisesShimNames(t *testing.T) {
	for argv0, want := range map[string]string{
		`C:\Users\me\AppData\Roaming\Flock\bin\php.exe`: "php",
		`C:\Users\me\AppData\Roaming\Flock\bin\PHP.EXE`: "php",
		"composer":             "composer",
		"/usr/local/bin/flock": "",
		`C:\x\flock.exe`:       "",
		`C:\x\flockd.exe`:      "",
	} {
		// filepath.Base only splits on the host separator; normalise for the test on macOS.
		if got := ShimTool(strings.ReplaceAll(argv0, `\`, "/")); got != want {
			t.Errorf("%s: got %q, want %q", argv0, got, want)
		}
	}
}
