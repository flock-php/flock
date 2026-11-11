package config

import (
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
