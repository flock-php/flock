package config

import (
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
