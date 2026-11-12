package config

import (
	"os"
	"strings"
	"testing"
)

// TestDomainUsesHostOverride pins the Host chokepoint: an empty Host keeps today's
// <name>.<suffix> byte-for-byte, and a Host replaces the name part of the primary host.
// Identity stays the Name: an override moves no config key, no CLI path, no folder.
func TestDomainUsesHostOverride(t *testing.T) {
	c := &Config{TLD: "test", Sites: []Site{
		{Name: "demo"},
		{Name: "cms-liga-local", Host: "liga"},
		{Name: "over", Host: "liga", TLD: "loc"},
	}}
	if got := c.Domain(c.Sites[0]); got != "demo.test" {
		t.Errorf(`Host "": %q, want demo.test (legacy byte-stability)`, got)
	}
	if got := c.Domain(c.Sites[1]); got != "liga.test" {
		t.Errorf("Host liga: %q, want liga.test", got)
	}
	if got := c.Domain(c.Sites[2]); got != "liga.loc" {
		t.Errorf("Host liga with its own suffix: %q, want liga.loc", got)
	}
	if _, ok := c.FindSite("liga"); ok {
		t.Error("FindSite must stay keyed on the identity Name, not the Host")
	}
	if _, ok := c.FindSite("cms-liga-local"); !ok {
		t.Error("the site must still be found under its Name")
	}
}

// TestLoadSanitisesHostAndTitle: config.json is hand-editable, so a stored Host or Title
// is input nobody screened — sanitizeSuffix's doctrine applies, per field. Since the
// 2026-11-12 relaxation the Host may be a dotted host name ("a.b" survives untouched);
// the refusal doctrine is unchanged: anything HostNameOK refuses is cleared to "".
func TestLoadSanitisesHostAndTitle(t *testing.T) {
	root := t.TempDir()
	body := `{"tld":"test","sites":[
		{"name":"a","path":"/code/a","host":"LIGA ","title":"  x\ty\nz  "},
		{"name":"b","path":"/code/b","host":"a.b"},
		{"name":"c","path":"/code/c","host":"","title":"x\ny"},
		{"name":"d","path":"/code/d","host":"localhost"},
		{"name":"e","path":"/code/e","host":"E"},
		{"name":"f","path":"/code/f","title":"` + strings.Repeat("é", 200) + `"},
		{"name":"g","path":"/code/g","host":"-bad-","title":"\t"},
		{"name":"h","path":"/code/h","host":" CMS.LIGA "},
		{"name":"i","path":"/code/i","host":"a..b"}
	]}`
	if err := os.WriteFile(file(root), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	wantHosts := []string{"liga", "a.b", "", "localhost", "", "", "", "cms.liga", ""}
	wantTitles := []string{"xyz", "", "xy", "", "", strings.Repeat("é", 80), "", "", ""}
	for i, s := range cfg.Sites {
		if s.Host != wantHosts[i] {
			t.Errorf("site %s: Host = %q, want %q", s.Name, s.Host, wantHosts[i])
		}
		if s.Title != wantTitles[i] {
			t.Errorf("site %s: Title = %q, want %q", s.Name, s.Title, wantTitles[i])
		}
	}

	// Host is a host name, not a suffix: HostNameOK is the only rule (no ValidateTLD
	// here), so "localhost" passes the label rule and stays — deliberate, as ever.
	if got := cfg.Domain(cfg.Sites[0]); got != "liga.test" {
		t.Errorf("a with the sanitized host: %q, want liga.test", got)
	}
	if got := cfg.Domain(cfg.Sites[1]); got != "a.b.test" {
		t.Errorf("b with the dotted host: %q, want a.b.test", got)
	}
	if got := cfg.Domain(cfg.Sites[4]); got != "e.test" {
		t.Errorf("host equal to Name: %q, want e.test (cleared to inherit)", got)
	}

	// Idempotence: the sanitizer never rewrites a stored valid host — Save what Load
	// produced, Load again, and every Host and Title is byte-identical.
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	again, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range again.Sites {
		if s.Host != cfg.Sites[i].Host || s.Title != cfg.Sites[i].Title {
			t.Errorf("second Load changed site %s: %q/%q vs %q/%q",
				s.Name, s.Host, s.Title, cfg.Sites[i].Host, cfg.Sites[i].Title)
		}
	}
}

// TestTitleIsDisplayOnly pins the spec's anti-goal: a title that changes any URL, config
// key or CLI path. Domain and the landing checks must answer identically with a Title set.
func TestTitleIsDisplayOnly(t *testing.T) {
	c := &Config{TLD: "test", Sites: []Site{
		{Name: "demo", Path: "/code/demo"},
		{Name: "shop", Path: "/code/shop", Domains: []Domain{{Label: "demo", Alias: true}}},
	}}
	host, owner := c.SiteHostCollision(c.Sites[0])
	if host != "demo.test" || owner != "site shop" {
		t.Fatalf("baseline collision = %q/%q, want demo.test held by site shop", host, owner)
	}

	c.Sites[0].Title = "The Shop"
	c.Sites[1].Title = "Shop Two"
	if h, o := c.SiteHostCollision(c.Sites[0]); h != host || o != owner {
		t.Errorf("Title changed the collision answer: %q/%q, want %q/%q", h, o, host, owner)
	}
	if got := c.Domain(c.Sites[0]); got != "demo.test" {
		t.Errorf("Title changed the domain: %q, want demo.test", got)
	}
}

// TestTLDMoveCollisionHost: the global suffix switch probes each site's Host-aware
// primary. The site cms-liga is served at liga.test, so on a switch to .loc it rides
// onto liga.loc — where the pinned site liga already holds the host — while without the
// Host it would land on the untouched demo-style name and the move would look clean.
func TestTLDMoveCollisionHost(t *testing.T) {
	c := &Config{TLD: "test", Sites: []Site{
		{Name: "cms-liga", Host: "liga"},
		{Name: "liga", TLD: "loc"},
	}}
	col := c.TLDMoveCollision("loc")
	if col == nil {
		t.Fatal("the move must report the collision the Host override creates")
	}
	if col.Host != "liga.loc" || col.Holder != "site liga" || col.Mover != "site cms-liga" {
		t.Fatalf("got %+v, want liga.loc held by site liga, moved on by site cms-liga", col)
	}
	// Onto .dev nothing rides onto liga.loc: clean.
	if col := c.TLDMoveCollision("dev"); col != nil {
		t.Errorf("onto dev must be clean, got %+v", col)
	}
}
