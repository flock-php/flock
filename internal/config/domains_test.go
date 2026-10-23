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
		"api.shop.test":        "api.shop.test",
		"v1.store.test":        "v1.store.test",
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
		"store.test":                       "already served by shop",
		strings.Repeat("a.", 130) + "test": "too long",
	} {
		got, err := cfg.NormalizeDomain(app, in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NormalizeDomain(%q) = %q, %v; want an error containing %q", in, got, err, want)
		}
	}
}

func TestNormalizeDomainAllowsNamesAboveAndBelowAnotherSitesNames(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app"})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []string{"eu.store.test", "admin"}})
	app, _ := cfg.FindSite("app")

	for _, in := range []string{"store.test", "x.eu.store.test", "accounts.shop.test", "x.admin.shop.test"} {
		if _, err := cfg.NormalizeDomain(app, in); err != nil {
			t.Errorf("NormalizeDomain(%q): %v", in, err)
		}
	}
	// shop lists admin.shop.test itself.
	if _, err := cfg.NormalizeDomain(app, "admin.shop.test"); err == nil || !strings.Contains(err.Error(), "shop") {
		t.Errorf("admin.shop.test: want a conflict with shop, got %v", err)
	}
}

func TestNormalizeDomainRejectsASubdomainAnotherSiteHasAsItsAlias(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop"})
	cfg.Upsert(Site{Name: "accounts", Path: "/code/accounts", Domains: []string{"accounts.shop.test"}})
	shop, _ := cfg.FindSite("shop")

	if _, err := cfg.NormalizeDomain(shop, "accounts"); err == nil || !strings.Contains(err.Error(), "accounts.shop.test is already served by accounts") {
		t.Fatalf("want a conflict with accounts, got %v", err)
	}
	if _, err := cfg.NormalizeDomain(shop, "x.accounts"); err != nil {
		t.Fatalf("x.accounts: %v", err)
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
	cfg.Upsert(Site{Name: "blog", Path: "/code/blog", Domains: []string{"shared.test"}})
	cfg.Upsert(Site{Name: "promo", Path: "/code/promo"})

	by := cfg.DomainsBySite(cfg.AllSites())
	if p := by["app"][0].Problem; !strings.Contains(p, "promo") {
		t.Errorf("promo.test: want a problem naming promo, got %q", p)
	}
	if p := by["app"][1].Problem; p != "" {
		t.Errorf("shared.test: app comes first, want no problem, got %q", p)
	}
	if p := by["blog"][0].Problem; !strings.Contains(p, "app") {
		t.Errorf("blog's shared.test: want a problem naming app, got %q", p)
	}
}

func TestDomainsBySiteServesAnAliasUnderAnotherSiteOverItsSubdomain(t *testing.T) {
	cfg := Default()
	// accounts.shop.test was linked before the shop project, and shop lists it as a subdomain too.
	cfg.Upsert(Site{Name: "accounts", Path: "/code/accounts", Domains: []string{"accounts.shop.test", "x.promo.test"}})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []string{"accounts", "tenant1"}})
	cfg.Upsert(Site{Name: "promo", Path: "/code/promo"})

	by := cfg.DomainsBySite(cfg.AllSites())
	for _, d := range by["accounts"] {
		if d.Problem != "" {
			t.Errorf("%s: want it served, got %q", d.Host, d.Problem)
		}
	}
	if p := by["shop"][0].Problem; p != "accounts.shop.test is served by accounts" {
		t.Errorf("shop's accounts: got %q", p)
	}
	if p := by["shop"][1].Problem; p != "" {
		t.Errorf("shop's tenant1: got %q", p)
	}
}
