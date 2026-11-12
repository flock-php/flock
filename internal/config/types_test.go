package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectType(t *testing.T) {
	devScript := `{"scripts": {"dev": "next dev"}}`
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"empty folder", nil, TypePHP},
		{"Next.js app", map[string]string{"package.json": devScript}, TypeNode},
		{"package.json without a dev script", map[string]string{"package.json": `{"scripts": {"build": "tsc"}}`}, TypePHP},
		{"broken package.json", map[string]string{"package.json": `{`}, TypePHP},
		{"Laravel with Vite", map[string]string{"package.json": `{"scripts": {"dev": "vite"}}`, "composer.json": "{}", "artisan": ""}, TypePHP},
		{"plain PHP with a dev script", map[string]string{"package.json": devScript, "public/index.php": ""}, TypePHP},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tc.files)
			if got := DetectType(dir); got != tc.want {
				t.Fatalf("DetectType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSitesWithoutATypeLoadAsPHP(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"config.json": `{"tld": "test", "sites": [{"name": "old", "path": "/code/old"}, {"name": "app", "path": "/code/app", "type": "node"}]}`})

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Type != TypePHP || cfg.Sites[1].Type != TypeNode {
		t.Fatalf("types: %q, %q", cfg.Sites[0].Type, cfg.Sites[1].Type)
	}

	// Saved, the old site has its type written out.
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "config.json"))
	if strings.Count(string(data), `"type": "php"`) != 1 {
		t.Fatalf("the old site isn't saved as php:\n%s", data)
	}

	if (Site{}).Kind() != TypePHP {
		t.Fatal("a site without a type isn't PHP")
	}
}

func TestParkedAndNewSitesGetTheTypeTheirFolderSuggests(t *testing.T) {
	parked := t.TempDir()
	writeFiles(t, parked, map[string]string{
		"shop/composer.json":   "{}",
		"web-app/package.json": `{"scripts": {"dev": "vite"}}`,
	})
	cfg := Default()
	cfg.Parked = []string{parked}

	for name, want := range map[string]string{"shop": TypePHP, "web-app": TypeNode} {
		if s, ok := cfg.FindSite(name); !ok || s.Type != want {
			t.Fatalf("%s: type %q, want %q", name, s.Type, want)
		}
	}

	// A new site without a type gets its folder's; a type that is set stays.
	if err := cfg.Upsert(Site{Name: "linked", Path: filepath.Join(parked, "web-app")}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Upsert(Site{Name: "forced", Path: filepath.Join(parked, "web-app"), Type: TypePHP}); err != nil {
		t.Fatal(err)
	}
	if s, _ := cfg.FindSite("linked"); s.Type != TypeNode {
		t.Fatalf("linked: %q", s.Type)
	}
	if s, _ := cfg.FindSite("forced"); s.Type != TypePHP {
		t.Fatalf("forced: %q", s.Type)
	}
	if err := cfg.Upsert(Site{Name: "odd", Path: parked, Type: "dotnet"}); err == nil {
		t.Fatal("an unknown type was stored")
	}
}

func TestSetTypeLinksTheSiteAndOnlyPHPSitesHaveAServer(t *testing.T) {
	cfg := Default()
	site := Site{Name: "app", Path: "/code/app", Parked: true, Server: ServerFrankenPHP, Port: FirstSitePort}
	if !site.FrankenPHP() {
		t.Fatal("a PHP site on FrankenPHP isn't")
	}

	if err := cfg.SetType(&site, TypeNode); err != nil || site.Parked || site.Type != TypeNode {
		t.Fatalf("SetType: %v, %+v", err, site)
	}
	// The PHP settings are kept for switching back, but don't apply to a Node.js site.
	if site.FrankenPHP() || site.Server != ServerFrankenPHP {
		t.Fatalf("server: %+v", site)
	}
	if err := cfg.SetServer(&site, ServerFrankenPHP, ""); err == nil || !strings.Contains(err.Error(), "Node.js site") {
		t.Fatalf("SetServer on a Node.js site: %v", err)
	}

	if err := cfg.SetType(&site, "python"); err == nil {
		t.Fatal("an unknown type was set")
	}
	if wt := (Site{Name: "feature.app", Parent: "app"}); cfg.SetType(&wt, TypeNode) == nil {
		t.Fatal("a worktree got a type of its own")
	}
}
