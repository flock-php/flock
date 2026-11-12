package config

import (
	"slices"
	"strings"
	"testing"
)

func TestNormalizeTLD(t *testing.T) {
	for in, want := range map[string]string{
		".LOC ":  "loc",
		"Loc":    "loc",
		"  ":     "",
		".test":  "test",
		"..loc":  "loc",
		".":      "",
		" TEST ": "test",
		"loc":    "loc",
	} {
		if got := NormalizeTLD(in); got != want {
			t.Errorf("NormalizeTLD(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateTLDAcceptsLocalLabels(t *testing.T) {
	for _, tld := range []string{"test", "loc", "home-ar", "internal", "example"} {
		warning, err := ValidateTLD(tld)
		if err != nil || warning != "" {
			t.Errorf("ValidateTLD(%q) = warning %q, err %v; want it accepted without warning", tld, warning, err)
		}
	}
	warning, err := ValidateTLD("local")
	if err != nil {
		t.Fatalf("ValidateTLD(\"local\"): %v; want it accepted with a warning", err)
	}
	if !strings.Contains(warning, "Bonjour") || !strings.Contains(warning, "mDNS") {
		t.Errorf("ValidateTLD(\"local\"): want the warning to mention Bonjour and mDNS, got %q", warning)
	}
}

// TestValidateTLDWarnsOnAltAndLocalhost: the picker offers these two and ValidateTLD is the
// rule for every entry point — accepted with a warning, never an error, so the suffix stays in
// ActiveSuffixes and gets provisioned (roots, resolvers, NRPT).
func TestValidateTLDWarnsOnAltAndLocalhost(t *testing.T) {
	for _, tld := range []string{"localhost", "alt"} {
		warning, err := ValidateTLD(tld)
		if err != nil || warning == "" {
			t.Errorf("ValidateTLD(%q) = warning %q, err %v; want it accepted with a warning", tld, warning, err)
		}
	}
}

func TestValidateTLDRejectsUnusableLabels(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "empty",
		"x":                     "not a valid",
		"a b":                   "not a valid",
		"-x":                    "not a valid",
		"x-":                    "not a valid",
		strings.Repeat("a", 64): "not a valid",
		"x.y":                   "one label",
	} {
		if _, err := ValidateTLD(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateTLD(%q): want an error containing %q, got %v", in, want, err)
		}
	}
}

func TestValidateTLDRejectsReservedAndPublic(t *testing.T) {
	for _, tld := range []string{"invalid"} {
		if _, err := ValidateTLD(tld); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("ValidateTLD(%q): want an error containing \"reserved\", got %v", tld, err)
		}
	}
	for _, tld := range []string{"dev", "app", "com", "io"} {
		if _, err := ValidateTLD(tld); err == nil || !strings.Contains(err.Error(), "public TLD") {
			t.Errorf("ValidateTLD(%q): want an error mentioning the public TLD, got %v", tld, err)
		}
	}
}

// TestSetTLDOnlySetsTheTLD: after the Load migration nothing is stored with a suffix baked in
// that the global change may move — inherited aliases resolve at use time and follow,
// subdomain labels carry no suffix, sites with a Site.TLD override and pinned aliases don't
// move. SetTLD itself touches nothing but c.TLD (ledger ruling R5: the string rewrite dies).
func TestSetTLDOnlySetsTheTLD(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []Domain{sub("tenant1"), sub("a.b"), inherited("app-2"), sub("x.example")}})
	cfg.Upsert(Site{Name: "over", Path: "/code/over", TLD: "loc", Domains: []Domain{pinned("p", "dev2"), inherited("i")}})

	if old := cfg.SetTLD("loc"); old != "test" {
		t.Fatalf("SetTLD(\"loc\") returned old %q, want \"test\"", old)
	}
	if cfg.TLD != "loc" {
		t.Errorf("c.TLD = %q, want \"loc\"", cfg.TLD)
	}
	app, _ := cfg.FindSite("app")
	wantEntries := []Domain{sub("tenant1"), sub("a.b"), inherited("app-2"), sub("x.example")}
	if !slices.Equal(app.Domains, wantEntries) {
		t.Errorf("after SetTLD(\"loc\"): got %+v, want the entries untouched", app.Domains)
	}
	if got := hostsOf(cfg, app); !slices.Equal(got, []string{"tenant1.app.loc", "a.b.app.loc", "app-2.loc", "x.example.app.loc"}) {
		t.Errorf("hosts under .loc: %v", got)
	}
	over, _ := cfg.FindSite("over")
	if got := hostsOf(cfg, over); !slices.Equal(got, []string{"p.dev2", "i.loc"}) {
		t.Errorf("override site's hosts under global .loc: %v, want unchanged (its suffix is its own)", got)
	}

	if old := cfg.SetTLD("test"); old != "loc" {
		t.Fatalf("SetTLD(\"test\") returned old %q, want \"loc\"", old)
	}
	if app, _ = cfg.FindSite("app"); !slices.Equal(app.Domains, wantEntries) {
		t.Errorf("after the round-trip: got %+v, want the original entries back", app.Domains)
	}
	if got := hostsOf(cfg, app); !slices.Equal(got, []string{"tenant1.app.test", "a.b.app.test", "app-2.test", "x.example.app.test"}) {
		t.Errorf("hosts after the round-trip: %v, want the original hosts back", got)
	}
}

func TestIsPublicTLD(t *testing.T) {
	for _, tld := range []string{"com", "net", "dev", "app", "io"} {
		if !IsPublicTLD(tld) {
			t.Errorf("IsPublicTLD(%q) = false; want true", tld)
		}
	}
	for _, tld := range []string{"test", "loc", "local", "localhost", "invalid", "home-ar"} {
		if IsPublicTLD(tld) {
			t.Errorf("IsPublicTLD(%q) = true; want false", tld)
		}
	}
}
