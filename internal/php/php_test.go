package php

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestXdebugLoadsOnlyInTheDebugPool(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root, "8.5"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := DebugPoolFor(root, "8.5"); err == nil || !strings.Contains(err.Error(), "without Xdebug") {
		t.Fatalf("missing xdebug.so must be explained, got %v", err)
	}

	so := ExtensionPath(root, "8.5", "xdebug")
	_ = os.MkdirAll(filepath.Dir(so), 0o755)
	_ = os.WriteFile(so, nil, 0o755)

	normal, err := PoolFor(root, "8.5")
	if err != nil {
		t.Fatal(err)
	}
	debug, err := DebugPoolFor(root, "8.5")
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(XdebugDir(root, "8.5"), "xdebug.ini"))
	if !strings.Contains(string(data), `zend_extension = "`+so+`"`) || !strings.Contains(string(data), "start_with_request = yes") {
		t.Fatalf("unexpected ini:\n%s", data)
	}

	scanDir := func(env []string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "PHP_INI_SCAN_DIR="); ok {
				return v
			}
		}
		return ""
	}
	if dir := scanDir(normal.Specs[0].Env); strings.Contains(dir, XdebugDir(root, "8.5")) {
		t.Fatalf("the normal pool scans %s", dir)
	}
	if dir := scanDir(debug.Specs[0].Env); !strings.Contains(dir, XdebugDir(root, "8.5")) {
		t.Fatalf("the debug pool scans %s", dir)
	}
	if slices.Equal(normal.Upstreams, debug.Upstreams) || normal.Specs[0].Name == debug.Specs[0].Name {
		t.Fatalf("pools collide: %v %v", normal.Upstreams, debug.Upstreams)
	}
}

func TestCGIPoolRunsWorkersOnDistinctPortsPerVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root, "8.4"), 0o755); err != nil {
		t.Fatal(err)
	}

	pool, err := CGIPool(root, "8.4", "VAR_DUMPER_FORMAT=server")
	if err != nil {
		t.Fatal(err)
	}
	if len(pool.Specs) != CGIWorkers || len(pool.Upstreams) != CGIWorkers {
		t.Fatalf("got %d specs, %d upstreams", len(pool.Specs), len(pool.Upstreams))
	}
	if pool.Upstreams[0] != "127.0.0.1:19840" || pool.Specs[1].Name != "php8.4-cgi-2" {
		t.Fatalf("got %v / %s", pool.Upstreams, pool.Specs[1].Name)
	}
	if args := pool.Specs[0].Args; args[0] != "-b" || args[1] != "127.0.0.1:19840" {
		t.Fatalf("args %v", args)
	}
	if !slices.Contains(pool.Specs[0].Env, "PHP_FCGI_MAX_REQUESTS=0") || !slices.Contains(pool.Specs[0].Env, "VAR_DUMPER_FORMAT=server") {
		t.Fatalf("env %v", pool.Specs[0].Env)
	}
	if CGIPort("8.5", 0) == CGIPort("8.4", 0) || CGIPort("8.4", CGIWorkers-1) >= CGIPort("8.5", 0) {
		t.Fatal("versions must not share ports")
	}
}

func TestDebugCGIPortsStayClearOfTheWorkers(t *testing.T) {
	for _, minor := range []string{"7.4", "8.0", "8.4", "8.5"} {
		for _, other := range []string{"7.4", "8.0", "8.4", "8.5"} {
			for w := range CGIWorkers {
				for dw := range DebugCGIWorkers {
					if CGIPort(other, w) == DebugCGIPort(minor, dw) {
						t.Fatalf("PHP %s debug worker %d uses PHP %s worker %d's port", minor, dw, other, w)
					}
				}
			}
		}
	}
}
