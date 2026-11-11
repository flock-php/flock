package config

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// domainEntry_test constructors keep the tables below readable:
// sub = a subdomain of the site's domain, inherited = an alias under the site's effective
// suffix (follows it), pinned = an alias under a suffix of its own (never moves).

func sub(label string) Domain         { return Domain{Label: label} }
func inherited(label string) Domain   { return Domain{Label: label, Alias: true} }
func pinned(label, tld string) Domain { return Domain{Label: label, TLD: tld, Alias: true} }

func hostsOf(c *Config, s Site) []string {
	var hs []string
	for _, e := range c.ExtraDomains(s) {
		hs = append(hs, e.Host)
	}
	return hs
}

// TestLoadMigratesLegacyDomainStrings pins the migration rule: a stored string ending in
// "."+global TLD becomes an inherited alias with the suffix cut off (Plan 1's follow-the-global
// behavior), every other string — multi-label ("x.example", "a.b") or plain ("tenant1") —
// becomes a subdomain. The hosts after migration are byte-identical to today's.
func TestLoadMigratesLegacyDomainStrings(t *testing.T) {
	root := t.TempDir()
	body := `{"tld":"test","sites":[{"name":"demo","path":"/code/demo","domains":["tenant1","app-2.test","x.example"]}]}`
	if err := os.WriteFile(file(root), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	demo := cfg.Sites[0]
	want := []Domain{sub("tenant1"), inherited("app-2"), sub("x.example")}
	if !slices.Equal(demo.Domains, want) {
		t.Errorf("migrated entries: %+v, want %+v", demo.Domains, want)
	}
	wantHosts := []string{"tenant1.demo.test", "app-2.test", "x.example.demo.test"}
	if got := hostsOf(cfg, demo); !slices.Equal(got, wantHosts) {
		t.Errorf("hosts after migration: %v, want %v", got, wantHosts)
	}

	// The structured form survives re-saving and reloading: migration is one-time.
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	again, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(again.Sites[0].Domains, want) {
		t.Errorf("after round trip: %+v, want %+v", again.Sites[0].Domains, want)
	}

	// The wire form is objects now; a bare string in JSON is legacy, an object is kept as is.
	data, err := json.Marshal(cfg.Sites[0].Domains)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `[{"label":"tenant1"},{"label":"app-2","alias":true},{"label":"x.example"}]`
	if string(data) != wantJSON {
		t.Errorf("marshaled domains:\n%s\nwant\n%s", data, wantJSON)
	}
}

// TestDomainAcceptsBothWireForms checks the wire compatibility: old config.json files carry
// plain strings, new ones objects; both decode, and only the migration step tells them apart.
func TestDomainAcceptsBothWireForms(t *testing.T) {
	var s Site
	body := `{"name":"demo","path":"/x","domains":["tenant1","app-2.test",{"label":"demo2","tld":"loc","alias":true}]}`
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	for i := range s.Domains {
		s.Domains[i].migrate("test")
	}
	want := []Domain{sub("tenant1"), inherited("app-2"), pinned("demo2", "loc")}
	if !slices.Equal(s.Domains, want) {
		t.Errorf("mixed forms migrated: %+v, want %+v", s.Domains, want)
	}

	var bad Domain
	if err := json.Unmarshal([]byte(`42`), &bad); err == nil {
		t.Error("a number accepted as a domain")
	}
}

// TestSetTLDFollowsInheritedAndNeverDragsPinned is the Q2 trap pinned: site demo inherits
// .test and takes demo2.loc as an alias. The global .test→.loc moves demo and the inherited
// alias; demo2.loc is explicit and stays. The second change (.loc→.test) is the trap: string
// inference would read "demo2.loc" as an inherited alias (it ends in the effective suffix)
// and drag it to demo2.test. The explicit marker forbids that.
func TestSetTLDFollowsInheritedAndNeverDragsPinned(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "demo", Path: "/code/demo"})
	site, _ := cfg.FindSite("demo")
	if _, err := cfg.AddDomain(&site, "demo2.loc"); err != nil { // explicit alias
		t.Fatal(err)
	}
	if _, err := cfg.AddDomain(&site, "app-2.test"); err != nil { // inherited alias
		t.Fatal(err)
	}
	cfg.Upsert(site)
	if want := []Domain{pinned("demo2", "loc"), inherited("app-2")}; !slices.Equal(site.Domains, want) {
		t.Fatalf("stored entries: %+v, want %+v", site.Domains, want)
	}
	if got := hostsOf(cfg, site); !slices.Equal(got, []string{"demo2.loc", "app-2.test"}) {
		t.Fatalf("hosts: %v, want [demo2.loc app-2.test]", got)
	}

	if old := cfg.SetTLD("loc"); old != "test" {
		t.Fatalf("SetTLD(\"loc\") returned %q", old)
	}
	if got := hostsOf(cfg, cfg.Sites[0]); !slices.Equal(got, []string{"demo2.loc", "app-2.loc"}) {
		t.Fatalf("hosts under .loc: %v, want [demo2.loc app-2.loc]", got)
	}

	if old := cfg.SetTLD("test"); old != "loc" {
		t.Fatalf("SetTLD(\"test\") returned %q", old)
	}
	if got := hostsOf(cfg, cfg.Sites[0]); !slices.Equal(got, []string{"demo2.loc", "app-2.test"}) {
		t.Errorf("hosts after the round trip: %v, want [demo2.loc app-2.test] — demo2.loc is explicit and must never move", got)
	}
	want := []Domain{pinned("demo2", "loc"), inherited("app-2")}
	if !slices.Equal(cfg.Sites[0].Domains, want) {
		t.Errorf("entries after the round trip: %+v, want %+v", cfg.Sites[0].Domains, want)
	}
}

// TestSiteTLDSuffixChangeLeavesSiteAlone: a site on its own suffix (and its inherited aliases,
// which follow the site, not the world) is untouched by a global TLD change; explicit aliases
// never move.
func TestSiteTLDSuffixChangeLeavesSiteAlone(t *testing.T) {
	cfg := &Config{
		TLD: "test",
		Sites: []Site{{
			Name: "demo", Path: "/code/demo", TLD: "loc",
			Domains: []Domain{sub("tenant1"), inherited("app-2"), pinned("shop", "dev2")},
		}},
	}
	demo := cfg.Sites[0]
	if got := cfg.Domain(demo); got != "demo.loc" {
		t.Fatalf("site domain: %q, want demo.loc", got)
	}
	wantHosts := []string{"tenant1.demo.loc", "app-2.loc", "shop.dev2"}
	if got := hostsOf(cfg, demo); !slices.Equal(got, wantHosts) {
		t.Fatalf("hosts: %v, want %v", got, wantHosts)
	}

	cfg.SetTLD("home")
	if got := cfg.Domain(cfg.Sites[0]); got != "demo.loc" {
		t.Errorf("domain after global .test→.home: %q, want demo.loc (override wins)", got)
	}
	if got := hostsOf(cfg, cfg.Sites[0]); !slices.Equal(got, wantHosts) {
		t.Errorf("hosts after global change: %v, want %v (inherited follows the SITE's suffix, which did not change)", got, wantHosts)
	}
}

// TestActiveSuffixes pins the provisioning set: global TLD ∪ site overrides ∪ alias TLDs,
// deduped, sorted; public and otherwise refused suffixes are never in it (NormalizeDomain
// refuses to store them, and a config built in memory that carries one is filtered here).
func TestActiveSuffixes(t *testing.T) {
	cfg := &Config{
		TLD: "test",
		Sites: []Site{
			{Name: "a", TLD: "loc", Domains: []Domain{inherited("i"), pinned("e", "dev2")}},
			{Name: "b", Domains: []Domain{pinned("p", "home-ar"), sub("plain"), inherited("x"), pinned("dup", "loc")}},
		},
	}
	if got := cfg.ActiveSuffixes(); !slices.Equal(got, []string{"dev2", "home-ar", "loc", "test"}) {
		t.Fatalf("ActiveSuffixes: %v", got)
	}

	cfg.Sites[0].Domains = append(cfg.Sites[0].Domains, pinned("web", "com"), pinned("u", "x"))
	if got := cfg.ActiveSuffixes(); !slices.Equal(got, []string{"dev2", "home-ar", "loc", "test"}) {
		t.Errorf("ActiveSuffixes with a public (com) and refused (x) suffix: %v, want them excluded", got)
	}
}

// TestNormalizeDomainClassifiesEntries pins the classification: under the site's effective
// suffix → inherited alias; under a validated non-public other suffix → explicit alias;
// public suffixes and refused ones stay subdomain labels (today's "x.example" behavior).
func TestNormalizeDomainClassifiesEntries(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app"})
	app, _ := cfg.FindSite("app")

	for in, want := range map[string]Domain{
		"demo2.loc":        pinned("demo2", "loc"),
		"tenant9":          sub("tenant9"),
		"x.com":            sub("x.com"), // public suffixes are not aliases
		"api.demo2.loc":    pinned("api.demo2", "loc"),
		"app-3.test":       inherited("app-3"),
		"tenant3.app.test": sub("tenant3"), // under the site's own domain: subdomain, as today
		"my-app-3.test.":   inherited("my-app-3"),
	} {
		got, err := cfg.NormalizeDomain(app, in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomain(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}

	// A site on its own suffix: its suffix is the inherit boundary, the global one is just
	// "some other validated suffix" and pins.
	over := Site{Name: "demo", Path: "/code/demo", TLD: "loc"}
	if got, err := cfg.NormalizeDomain(over, "a.loc"); err != nil || got != inherited("a") {
		t.Errorf("on .loc site, NormalizeDomain(\"a.loc\") = %+v, %v; want inherited alias", got, err)
	}
	if got, err := cfg.NormalizeDomain(over, "a.test"); err != nil || got != pinned("a", "test") {
		t.Errorf("on .loc site, NormalizeDomain(\"a.test\") = %+v, %v; want explicit alias (global suffix is not the site's)", got, err)
	}
}

// TestExtraDomainsResolvesUnderSiteSuffix pins the API-facing strings: an inherited alias's
// Entry is a full host (like today's "app-2.test"), a subdomain's Entry is the bare label.
func TestExtraDomainsResolvesUnderSiteSuffix(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "demo", Path: "/code/demo", TLD: "loc", Domains: []Domain{
		sub("tenant1"), inherited("app-2"), pinned("shop", "dev2"),
	}})
	demo, _ := cfg.FindSite("demo")

	got := cfg.ExtraDomains(demo)
	want := []ExtraDomain{
		{Entry: "tenant1", Host: "tenant1.demo.loc"},
		{Entry: "app-2.loc", Host: "app-2.loc", Alias: true},
		{Entry: "shop.dev2", Host: "shop.dev2", Alias: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

// TestLoadSanitisesSiteAndDomainSuffixes: Site.TLD and Domain.TLD in the hand-editable file
// are input — Plan 1's rules apply, with "bad → inherit" instead of falling back to "test".
func TestLoadSanitisesSiteAndDomainSuffixes(t *testing.T) {
	root := t.TempDir()
	body := `{"tld":"test","sites":[
		{"name":"a","path":"/code/a","tld":" .LOC ","domains":[{"label":"d1","tld":"loc","alias":true},{"label":"d2","tld":"com","alias":true}]},
		{"name":"b","path":"/code/b","tld":"dev","domains":[{"label":"d3","tld":"x","alias":true}]},
		{"name":"c","path":"/code/c","tld":"local"}
	]}`
	if err := os.WriteFile(file(root), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	wantSites := []string{"loc", "", "local"}
	for i, want := range wantSites {
		if got := cfg.Sites[i].TLD; got != want {
			t.Errorf("site %s: TLD = %q, want %q", cfg.Sites[i].Name, got, want)
		}
	}
	a := cfg.Sites[0]
	if got := a.Domains[0]; got != pinned("d1", "loc") {
		t.Errorf("valid Domain.TLD: %+v, want it kept", got)
	}
	if got := a.Domains[1]; got != inherited("d2") {
		t.Errorf("public Domain.TLD: %+v, want it cleared to inherit", got)
	}
	if got := cfg.Sites[1].Domains[0]; got != inherited("d3") {
		t.Errorf("refused Domain.TLD: %+v, want it cleared to inherit", got)
	}

	// The effective domains are what the sites are served at.
	if got := cfg.Domain(cfg.Sites[0]); got != "a.loc" {
		t.Errorf("a under .LOC: %q, want a.loc", got)
	}
	if got := cfg.Domain(cfg.Sites[1]); got != "b.test" {
		t.Errorf("b under refused dev: %q, want b.test (inherit)", got)
	}
}

// TestAddDomainDedupsByHost: adding the same resolved host in any accepted form is refused.
func TestAddDomainDedupsByHost(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []Domain{inherited("app-2"), sub("tenant1")}})
	app, _ := cfg.FindSite("app")

	for _, in := range []string{"app-2.test", "tenant1", "tenant1.app.test"} {
		if _, err := cfg.AddDomain(&app, in); err == nil || !strings.Contains(err.Error(), "already an extra domain") {
			t.Errorf("AddDomain(%q): %v, want an \"already an extra domain\" error", in, err)
		}
	}
	// The same label under a different suffix is a different host and is accepted.
	if got, err := cfg.AddDomain(&app, "app-2.loc"); err != nil || got.Host != "app-2.loc" {
		t.Errorf("AddDomain(\"app-2.loc\"): %+v, %v; want host app-2.loc", got, err)
	}
}

func TestSiteSuffixAndDomain(t *testing.T) {
	cfg := &Config{TLD: "test", Sites: []Site{{Name: "demo"}, {Name: "over", TLD: "loc"}}}
	got := cfg.Domain(cfg.Sites[0]) + " " + cfg.Domain(cfg.Sites[1])
	if got != "demo.test over.loc" {
		t.Errorf("domains: %q, want \"demo.test over.loc\"", got)
	}
	if got := cfg.Sites[1].Suffix(cfg); got != "loc" {
		t.Errorf("Suffix: %q, want loc", got)
	}
}
