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
	for _, tld := range []string{"test", "loc", "home-ar"} {
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
	for _, tld := range []string{"localhost", "invalid"} {
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

func TestSetTLDMigratesStoredAliases(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []string{"tenant1", "a.b", "app-2.test", "x.example"}})

	if old := cfg.SetTLD("loc"); old != "test" {
		t.Fatalf("SetTLD(\"loc\") returned old %q, want \"test\"", old)
	}
	if cfg.TLD != "loc" {
		t.Errorf("c.TLD = %q, want \"loc\"", cfg.TLD)
	}
	app, _ := cfg.FindSite("app")
	want := []string{"tenant1", "a.b", "app-2.loc", "x.example"}
	if !slices.Equal(app.Domains, want) {
		t.Errorf("after SetTLD(\"loc\"): got %v, want %v", app.Domains, want)
	}

	if old := cfg.SetTLD("test"); old != "loc" {
		t.Fatalf("SetTLD(\"test\") returned old %q, want \"loc\"", old)
	}
	if app, _ = cfg.FindSite("app"); !slices.Equal(app.Domains, []string{"tenant1", "a.b", "app-2.test", "x.example"}) {
		t.Errorf("after the round-trip: got %v, want the original domains back", app.Domains)
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
