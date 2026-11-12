package config

import (
	"strings"
	"testing"
)

func domainsConfig() *Config {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []Domain{sub("tenant1"), inherited("app-2")}})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []Domain{inherited("store")}})
	return cfg
}

func TestNormalizeDomainStoresSubdomainsRelativeAndAliasesInFull(t *testing.T) {
	cfg := domainsConfig()
	app, _ := cfg.FindSite("app")

	for in, want := range map[string]Domain{
		"tenant2":             sub("tenant2"),
		" A.B ":               sub("a.b"),
		"tenant3.app.test":    sub("tenant3"),
		"a.b.c.d":             sub("a.b.c.d"),
		"My-App-3.TEST":       inherited("my-app-3"),
		"deep.alias.test":     inherited("deep.alias"),
		"x.y.z.w.app.test":    sub("x.y.z.w"),
		"tenant-with-dash-9":  sub("tenant-with-dash-9"),
		"test":                sub("test"),
		"api.v2.store-x.test": inherited("api.v2.store-x"),
		"tenant1.app-3.test":  inherited("tenant1.app-3"),
		"Upper.Case.App.Test": sub("upper.case"),
		"api.shop.test":       inherited("api.shop"),
		"v1.store.test":       inherited("v1.store"),
	} {
		got, err := cfg.NormalizeDomain(app, in)
		if err != nil || got != want {
			t.Errorf("NormalizeDomain(%q) = %+v, %v; want %+v", in, got, err, want)
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
		"sub.domain.":                      "leading or trailing dot",
		".foo":                             "leading or trailing dot",
		".":                                "leading or trailing dot",
		strings.Repeat("a.", 130) + "test": "too long",
	} {
		got, err := cfg.NormalizeDomain(app, in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NormalizeDomain(%q) = %+v, %v; want an error containing %q", in, got, err, want)
		}
	}
}

func TestNormalizeDomainAllowsNamesAboveAndBelowAnotherSitesNames(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "app", Path: "/code/app"})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []Domain{inherited("eu.store"), sub("admin")}})
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
	cfg.Upsert(Site{Name: "accounts", Path: "/code/accounts", Domains: []Domain{inherited("accounts.shop")}})
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
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []Domain{inherited("promo"), inherited("shared")}})
	cfg.Upsert(Site{Name: "blog", Path: "/code/blog", Domains: []Domain{inherited("shared")}})
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
	cfg.Upsert(Site{Name: "accounts", Path: "/code/accounts", Domains: []Domain{inherited("accounts.shop"), inherited("x.promo")}})
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", Domains: []Domain{sub("accounts"), sub("tenant1")}})
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

// TestDomainsBySiteConflictsAcrossSiteSuffixes: an explicit alias collides with a site that
// carries that host as its own domain (its override makes it primary), and an inherited alias
// under one site collides with the primary domain of a site that overrides to the same suffix.
func TestDomainsBySiteConflictsAcrossSiteSuffixes(t *testing.T) {
	cfg := Default()
	cfg.Upsert(Site{Name: "shop", Path: "/code/shop", TLD: "loc"})
	cfg.Upsert(Site{Name: "app", Path: "/code/app", Domains: []Domain{pinned("shop", "loc"), inherited("media")}})

	by := cfg.DomainsBySite(cfg.AllSites())
	if p := by["app"][0].Problem; !strings.Contains(p, "shop") {
		t.Errorf("shop.loc: want a problem naming shop, got %q", p)
	}
	if p := by["app"][1].Problem; p != "" {
		t.Errorf("media.test: app inherits .test, want it served, got %q", p)
	}
}

// TestHostLabelOK pins the one exported DNS-label rule: 1 to 63 characters of a-z, 0-9 and
// inner hyphens — what config's hostLabel checks for every domain label, now shared by the
// api's and the cli's label-only domain adds (ruling R7) instead of three regex copies.
func TestHostLabelOK(t *testing.T) {
	for _, good := range []string{"a", "1", "app-2", "tenant1", "demo2", "9z9", strings.Repeat("a", 63)} {
		if !HostLabelOK(good) {
			t.Errorf("%q: want a valid label", good)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "a--", "AbC", "a.b", "a_b", "a b", strings.Repeat("a", 64)} {
		if HostLabelOK(bad) {
			t.Errorf("%q: want an invalid label", bad)
		}
	}
}

// TestHostNameOK pins the site HOST name rule (the 2026-11-12 relaxation): one or more
// labels joined by single dots, each label HostLabelOK's shape, 253 total at most;
// leading, trailing and doubled dots refused. Strict lowercase, exactly like HostLabelOK
// — normalizing the input is the caller's job (trim+lowercase before the check).
func TestHostNameOK(t *testing.T) {
	good := []string{
		"liga", "cms.liga", "a", "1", "app-2", "x.y.z",
		strings.Repeat("a", 63), // one label at the cap
		strings.Repeat("a", 63) + "." + strings.Repeat("b", 63),
		strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61), // exactly 253
	}
	for _, host := range good {
		if !HostNameOK(host) {
			t.Errorf("%q: want a valid host name", host)
		}
	}
	bad := []string{
		"", "liga.", ".liga", "a..b", ".", "..",
		"LIGA", "CMS.LIGA", // strict lowercase: the caller normalizes first
		"-liga", "liga-", "a_b", "a b",
		strings.Repeat("a", 64), // one label over 63
		"a." + strings.Repeat("b", 64),
		strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62), // 254 total
	}
	for _, host := range bad {
		if HostNameOK(host) {
			t.Errorf("%q: want an invalid host name", host)
		}
	}
}
