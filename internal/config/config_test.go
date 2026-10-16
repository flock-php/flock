package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReturnsDefaultsWhenMissingAndRoundTrips(t *testing.T) {
	root := t.TempDir()

	cfg, err := Load(root)
	if err != nil || cfg.TLD != "test" || cfg.Ports.HTTPS != 443 {
		t.Fatalf("unexpected defaults: %+v, %v", cfg, err)
	}

	cfg.Upsert(Site{Name: "app", Path: "/srv/app", Secured: true})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	again, err := Load(root)
	if err != nil || len(again.Sites) != 1 || !again.Sites[0].Secured {
		t.Fatalf("round trip failed: %+v, %v", again, err)
	}
}

func TestAllSitesMergesParkedAndLinkedSites(t *testing.T) {
	parked := t.TempDir()
	for _, d := range []string{"Blog", "shop", ".hidden"} {
		if err := os.Mkdir(filepath.Join(parked, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := Default()
	cfg.Parked = []string{parked}
	cfg.Upsert(Site{Name: "shop", Path: "/elsewhere/shop", PHP: "8.4"})

	sites := cfg.AllSites()
	if len(sites) != 2 || sites[0].Name != "blog" || !sites[0].Parked || sites[1].Path != "/elsewhere/shop" {
		t.Fatalf("unexpected sites: %+v", sites)
	}
	if cfg.PHPFor(sites[0]) != "8.5" || cfg.PHPFor(sites[1]) != "8.4" {
		t.Fatal("wrong PHP resolution")
	}
}

func TestSiteForDirPicksDeepestMatch(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "outer", Path: "/code"})
	cfg.Upsert(Site{Name: "inner", Path: "/code/inner"})

	if s, ok := cfg.SiteForDir("/code/inner/app/Models"); !ok || s.Name != "inner" {
		t.Fatalf("want inner, got %+v", s)
	}
	if _, ok := cfg.SiteForDir("/codex"); ok {
		t.Fatal("/codex must not match /code")
	}
}

func TestSiteNameSanitisesDirectoryNames(t *testing.T) {
	if got := SiteName("My_App.v2"); got != "my-app-v2" {
		t.Fatalf("got %q", got)
	}
}

func TestMigrateMovesRedisToValkeyOnMacOS(t *testing.T) {
	data := []byte(`{"ports":{"redis":6380},"services":{"redis":"9.1.2","stopped":["redis","postgres"]}}`)
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatal(err)
	}
	if err := migrate(cfg, data, "darwin"); err != nil {
		t.Fatal(err)
	}
	if cfg.Services.Valkey != "9.1.2" || cfg.Ports.Valkey != 6380 || cfg.Services.Running("valkey") || !cfg.Services.Running("redis") || cfg.Services.Running("postgres") {
		t.Fatalf("unexpected migration: %+v %+v", cfg.Services, cfg.Ports)
	}
}

func TestMigrateMakesRedisACatalogServiceOnWindows(t *testing.T) {
	data := []byte(`{"ports":{"redis":6379},"services":{"redis":"7.2.16","stopped":["redis"]}}`)
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatal(err)
	}
	if err := migrate(cfg, data, "windows"); err != nil {
		t.Fatal(err)
	}
	if x, ok := cfg.Services.Extra("redis"); !ok || x.Version != "7.2.16" || x.Port != 6379 || cfg.Services.Valkey != "" || cfg.Services.Running("redis") {
		t.Fatalf("unexpected migration: %+v", cfg.Services)
	}
}

func TestMigrateLeavesNewConfigsAlone(t *testing.T) {
	data := []byte(`{"ports":{"valkey":6390},"services":{"valkey":"9.1.2","extras":{"redis":{"version":"8.2.1","port":6380}}}}`)
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatal(err)
	}
	if err := migrate(cfg, data, "darwin"); err != nil {
		t.Fatal(err)
	}
	if x, _ := cfg.Services.Extra("redis"); cfg.Ports.Valkey != 6390 || x.Port != 6380 {
		t.Fatalf("unexpected: %+v %+v", cfg.Ports, cfg.Services)
	}
	if used := cfg.UsedPorts(""); used[6380] != "redis" || used[6390] != "valkey" || used[443] != "web" {
		t.Fatalf("used ports: %v", used)
	}
}

func TestXdebugDetectionIsOnForOlderConfigs(t *testing.T) {
	root := t.TempDir()
	// "xdebug" loaded Xdebug for every request and was off by default; detection replaces it.
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"tld":"test","xdebug":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Xdebug || !cfg.XdebugBreakpoints {
		t.Fatalf("got xdebug %v, breakpoints %v", cfg.Xdebug, cfg.XdebugBreakpoints)
	}
}

func TestSetServerAssignsStablePortsAndValidates(t *testing.T) {
	cfg := Default()
	cfg.Services.SetExtra("mysql", Extra{Version: "8.4", Port: FirstSitePort})

	a := Site{Name: "a", Path: "/srv/a", Parked: true}
	if err := cfg.SetServer(&a, ServerFrankenPHP, WorkerOctane); err != nil {
		t.Fatal(err)
	}
	if a.Parked || !a.FrankenPHP() || a.Worker != WorkerOctane || a.Port != FirstSitePort+1 {
		t.Fatalf("a: %+v (the first port is MySQL's)", a)
	}
	cfg.Upsert(a)

	b := Site{Name: "b", Path: "/srv/b"}
	if err := cfg.SetServer(&b, ServerFrankenPHP, ""); err != nil || b.Port != FirstSitePort+2 {
		t.Fatalf("b: %+v, %v", b, err)
	}
	cfg.Upsert(b)

	// Switching back keeps the port and the worker mode; switching again reuses them.
	if err := cfg.SetServer(&a, ServerFastCGI, WorkerOctane); err != nil || a.Server != "" || a.Port != FirstSitePort+1 {
		t.Fatalf("back to FastCGI: %+v, %v", a, err)
	}
	cfg.Upsert(a)
	if err := cfg.SetServer(&a, ServerFrankenPHP, WorkerOctane); err != nil || a.Port != FirstSitePort+1 {
		t.Fatalf("again: %+v, %v", a, err)
	}
	if got := cfg.UsedPorts("")[FirstSitePort+2]; got != "frankenphp-b" {
		t.Errorf("UsedPorts names b's port %q", got)
	}

	if err := cfg.SetServer(&a, "swoole", ""); err == nil {
		t.Error("unknown server accepted")
	}
	if err := cfg.SetServer(&a, ServerFrankenPHP, "roadrunner"); err == nil {
		t.Error("unknown worker mode accepted")
	}
}
