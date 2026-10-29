package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"flock/internal/catalog"
	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/php"
	"flock/internal/postgres"
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

	cfg := withDefaults()
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
	s := &state{root: root, cfg: withDefaults()}

	path, argv, _, err := resolveTool(s, "/x", "php", nil)
	if err != nil || argv[0] != path || !filepath.IsAbs(argv[0]) {
		t.Fatalf("PHP_BINARY would not point at Flock's PHP: argv=%v err=%v", argv, err)
	}
}

func TestResolveToolRunsComposerThroughPHP(t *testing.T) {
	root := t.TempDir()
	fakePHP(t, root, "8.5")
	s := &state{root: root, cfg: withDefaults()}

	_, argv, _, err := resolveTool(s, "/x", "composer", []string{"install"})
	if err != nil || argv[1] != php.ComposerPhar(root) || argv[2] != "install" {
		t.Fatalf("got %v, %v", argv, err)
	}
}

func TestResolveToolExplainsMissingVersions(t *testing.T) {
	s := &state{root: t.TempDir(), cfg: withDefaults()}

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

	cfg := withDefaults()
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
	s := &state{root: root, cfg: withDefaults()}
	if _, _, _, err := resolveTool(s, "/x", "psql", nil); !errors.Is(err, errPostgresMissing) {
		t.Fatalf("no PostgreSQL installed: %v", err)
	}

	// The client tools are those of the installed server (psql.exe on Windows).
	bin := postgres.BinDir(root, "18.6-build1")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"postgres", "psql"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(bin, name), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	psql, _ := postgres.ClientTool(root, "psql")
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
	touchAll := func(files ...string) {
		for _, f := range files {
			if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f, nil, 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := withDefaults()
	s := &state{root: root, cfg: cfg}
	if _, _, _, err := resolveTool(s, "/x", "redis-cli", nil); !errors.Is(err, errRedisMissing) {
		t.Fatalf("nothing installed: %v", err)
	}

	// valkey-cli, and redis-cli without Redis, talk to Valkey on its port.
	cli := catalog.CLI(root, "valkey", "9.1.2")
	touchAll(cli)
	cfg.Services.SetExtra("valkey", config.Extra{Version: "9.1.2", Port: 6380})
	_, argv, _, err := resolveTool(s, "/x", "redis-cli", []string{"ping"})
	if err != nil || argv[0] != cli || strings.Join(argv[1:], " ") != "-p 6380 ping" {
		t.Fatalf("got %v %v", argv, err)
	}

	// With Redis installed, redis-cli talks to it; valkey-cli still to Valkey.
	redisCLI := catalog.CLI(root, "redis", "8.2.2")
	touchAll(redisCLI)
	cfg.Services.SetExtra("redis", config.Extra{Version: "8.2.2", Port: 6381})
	path, argv, _, err := resolveTool(s, "/x", "redis-cli", []string{"ping"})
	if err != nil || path != redisCLI || strings.Join(argv[1:], " ") != "-p 6381 ping" {
		t.Fatalf("got %s %v %v", path, argv, err)
	}
	if path, _, _, _ := resolveTool(s, "/x", "valkey-cli", nil); path != cli {
		t.Fatalf("valkey-cli resolved to %s", path)
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

func TestMissingRedisAndPostgresClientAreOfferedOnlyWhereFlockCanDownloadThem(t *testing.T) {
	s := &state{root: t.TempDir(), cfg: withDefaults()}
	for tool, want := range map[string]error{"redis-cli": errRedisMissing, "pg_dump": errPostgresMissing} {
		if _, _, _, err := resolveTool(s, "/x", tool, nil); !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", tool, err, want)
		}
	}
	if runtime.GOOS == "windows" {
		return // offerInstall would prompt
	}
	for _, err := range []error{errRedisMissing, errPostgresMissing} {
		if installed, installErr := offerInstall(context.Background(), s, err); installed || installErr != nil {
			t.Fatalf("macOS has no prebuilt build to offer: %v %v", installed, installErr)
		}
	}
}

// withDefaults is a config with PHP 8.5 and Node.js 22 as the defaults, as the first run sets them.
func withDefaults() *config.Config {
	cfg := config.Default()
	cfg.DefaultPHP, cfg.DefaultNode = "8.5", "22"
	return cfg
}

func TestResolveToolExplainsThatNothingIsInstalledYet(t *testing.T) {
	s := &state{root: t.TempDir(), cfg: config.Default()}

	if _, _, _, err := resolveTool(s, "/x", "php", nil); !errors.Is(err, errNoDefaultPHP) {
		t.Fatalf("php: got %v", err)
	}
	if _, _, _, err := resolveTool(s, "/x", "node", nil); !errors.Is(err, errNoDefaultNode) {
		t.Fatalf("node: got %v", err)
	}
}
