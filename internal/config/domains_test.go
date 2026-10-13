package config

import (
	"strings"
	"testing"
)

func domainsConfig() *Config {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []string{"tenant1", "app-2.test"}})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []string{"store.test"}})
	return cfg
}

func TestNormalizeDomainStoresSubdomainsRelativeAndAliasesInFull(t *testing.T) {
	cfg := domainsConfig()
	app, _ := cfg.FindSite("app")

	for in, want := range map[string]string{
		"tenant2":              "tenant2",
		" A.B ":                "a.b",
		"tenant3.app.test":     "tenant3",
		"a.b.c.d":              "a.b.c.d",
		"my-app-3.test.":       "my-app-3.test",
		"deep.alias.test":      "deep.alias.test",
		"x.y.z.w.app.test":     "x.y.z.w",
		"tenant-with-dash-9":   "tenant-with-dash-9",
		"test":                 "test",
		"api.v2.store-x.test":  "api.v2.store-x.test",
		"tenant1.app-3.test":   "tenant1.app-3.test",
		"Upper.Case.App.Test.": "upper.case",
	} {
		got, err := cfg.NormalizeDomain(app, in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeDomainRejectsConflictsAndInvalidNames(t *testing.T) {
	cfg := domainsConfig()
	app, _ := cfg.FindSite("app")

	for in, want := range map[string]string{
		"":                                 "empty",
		"*.tenant":                         "wildcards aren't needed",
		"https://x.test":                   "not a domain name",
		"x.test:443":                       "not a domain name",
		"app.test":                         "own domain",
		"tenant1":                          "already an extra domain",
		"app-2.test":                       "already an extra domain",
		"a.b.c.d.e":                        "5 levels below app.test",
		"under_score":                      "not a valid domain name",
		"-dash":                            "not a valid domain name",
		"shop.test":                        "already served by shop",
		"api.shop.test":                    "already served by shop",
		"store.test":                       "already served by shop",
		"v1.store.test":                    "already served by shop",
		strings.Repeat("a.", 130) + "test": "too long",
	} {
		got, err := cfg.NormalizeDomain(app, in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want an error containing %q", in, got, err, want)
		}
	}
}

func TestNormalizeDomainRejectsAnAliasThatWouldSwallowAnotherSitesAlias(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app"})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []string{"eu.store.test"}})
	app, _ := cfg.FindSite("app")

	if _, err := cfg.NormalizeDomain(app, "store.test"); err == nil || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("want a conflict with shop's eu.store.test, got %v", err)
	}
}

func TestExtraDomainsResolveToFullNames(t *testing.T) {
	cfg := domainsConfig()
	app, _ := cfg.FindSite("app")

	got := cfg.ExtraDomains(app)
	if len(got) != 2 ||
		got[0] != (ExtraDomain{Entry: "tenant1", Host: "tenant1.app.test"}) ||
		got[1] != (ExtraDomain{Entry: "app-2.test", Host: "app-2.test", Alias: true}) {
		t.Fatalf("got %+v", got)
	}
}

func TestDomainsBySiteMarksAliasesOtherSitesServe(t *testing.T) {
	cfg := Default()
	// A parked directory named "promo" appears after app took promo.test as an alias,
	// and two sites claim the same alias.
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []string{"promo.test", "shared.test"}})
	cfg.Upsert(Site{Name: "blog", Path: "/code/blog", Domains: []string{"shared.test", "x.shared.test"}})
	cfg.Upsert(Site{Name: "promo", Path: "/code/promo"})

	by := cfg.DomainsBySite(cfg.AllSites())
	if p := by["app"][0].Problem; !strings.Contains(p, "promo") {
		t.Errorf("promo.test: want a problem naming promo, got %q", p)
	}
	if p := by["app"][1].Problem; p != "" {
		t.Errorf("shared.test: app comes first, want no problem, got %q", p)
	}
	for _, d := range by["blog"] {
		if !strings.Contains(d.Problem, "app") {
			t.Errorf("%s: want a problem naming app, got %q", d.Host, d.Problem)
		}
	}
}
