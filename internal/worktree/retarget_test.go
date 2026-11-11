package worktree

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

func TestRetargetDotenvSwapsHostsAndKeepsEverythingElse(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, strings.Join([]string{
		"# built when the site was acme.test (before that: old.test)",
		`APP_NAME="Acme"`,
		"APP_URL=http://acme.test # the site",
		"SESSION_DOMAIN=.acme.test",
		"SANCTUM_STATEFUL_DOMAINS=localhost,acme.test:5173",
		"MAIL_FROM=old@acme.test",
		"REDIS_PREFIX=acme_",
		"",
	}, "\n"))

	changed, err := RetargetDotenv(path, "acme.test", "acme.loc")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"# built when the site was acme.test (before that: old.test)",
		`APP_NAME="Acme"`,
		"APP_URL=http://acme.loc # the site",
		"SESSION_DOMAIN=.acme.loc",
		"SANCTUM_STATEFUL_DOMAINS=localhost,acme.loc:5173",
		"MAIL_FROM=old@acme.loc",
		"REDIS_PREFIX=acme_",
		"",
	}, "\n")
	if got := read(t, path); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	wantKeys := []string{"APP_URL", "SESSION_DOMAIN", "SANCTUM_STATEFUL_DOMAINS", "MAIL_FROM"}
	if !slices.Equal(changed, wantKeys) {
		t.Errorf("changed: %v, want %v", changed, wantKeys)
	}

	// A second call finds nothing left to swap and leaves the file alone.
	before := read(t, path)
	changed, err = RetargetDotenv(path, "acme.test", "acme.loc")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Errorf("second call changed keys: %v", changed)
	}
	if got := read(t, path); got != before {
		t.Errorf("second call rewrote the file:\n%s", got)
	}
}

func TestRetargetDotenvKeepsHostsThatOnlyLookSimilar(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, "APP_URL=https://app.test.evil.com\nSANCTUM_STATEFUL_DOMAINS=acme.test.evil.com,evil-acme.test\nMAIL_FROM=old@acme.test.evil.com\nDB_HOST=acme.test\n")

	changed, err := RetargetDotenv(path, "acme.test", "acme.loc")
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Errorf("changed keys: %v", changed)
	}
	if got := read(t, path); got != "APP_URL=https://app.test.evil.com\nSANCTUM_STATEFUL_DOMAINS=acme.test.evil.com,evil-acme.test\nMAIL_FROM=old@acme.test.evil.com\nDB_HOST=acme.test\n" {
		t.Errorf("file changed:\n%s", got)
	}
}

func TestRetargetDotenvSwapsDeeperHostsInsideURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, "APP_URL=https://x.acme.test/app\nASSET_URL=http://cdn.acme.test:8080/a?b=c\n")

	changed, err := RetargetDotenv(path, "acme.test", "acme.loc")
	if err != nil {
		t.Fatal(err)
	}
	// ASSET_URL is not one of the keys a site's host lives in: only APP_URL swaps.
	if !slices.Equal(changed, []string{"APP_URL"}) {
		t.Errorf("changed: %v, want [APP_URL]", changed)
	}
	want := "APP_URL=https://x.acme.loc/app\nASSET_URL=http://cdn.acme.test:8080/a?b=c\n"
	if got := read(t, path); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRetargetDotenvKeepsQuotingExportAndComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	write(t, path, "export MAIL_FROM=\"old@acme.test\" # the sender\nAPP_URL='https://x.acme.test/y'\nSESSION_DOMAIN=\n")

	changed, err := RetargetDotenv(path, "acme.test", "acme.loc")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changed, []string{"MAIL_FROM", "APP_URL"}) {
		t.Errorf("changed: %v", changed)
	}
	want := "export MAIL_FROM=\"old@acme.loc\" # the sender\nAPP_URL='https://x.acme.loc/y'\nSESSION_DOMAIN=\n"
	if got := read(t, path); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRetargetDotenvMissingFileIsANoop(t *testing.T) {
	changed, err := RetargetDotenv(filepath.Join(t.TempDir(), ".env"), "acme.test", "acme.loc")
	if err != nil {
		t.Fatal(err)
	}
	if changed != nil {
		t.Errorf("changed: %v, want nil", changed)
	}
}

func TestTLDHostSwapsListsEverySiteAndWorktree(t *testing.T) {
	siteDir, wtDir, bareDir := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, filepath.Join(siteDir, ".env"), "SESSION_DOMAIN=acme.test\n")
	// wtDir and bareDir have no .env: their swaps must be no-ops, not errors.

	cfg := &config.Config{
		TLD:       "loc", // already migrated: the swap list must come from the arguments, not c.TLD
		Sites:     []config.Site{{Name: "acme", Path: siteDir}, {Name: "bare", Path: bareDir}},
		Worktrees: []config.WorktreeEntry{{Path: wtDir, Site: "acme", Slug: "feature-x"}},
	}
	swaps := TLDHostSwaps(cfg, "test", "loc")
	want := []HostSwap{
		{Path: filepath.Join(siteDir, ".env"), Old: "acme.test", New: "acme.loc"},
		{Path: filepath.Join(bareDir, ".env"), Old: "bare.test", New: "bare.loc"},
		{Path: filepath.Join(wtDir, ".env"), Old: "feature-x.acme.test", New: "feature-x.acme.loc"},
	}
	if !slices.Equal(swaps, want) {
		t.Errorf("got %+v\nwant %+v", swaps, want)
	}

	for _, sw := range swaps {
		if _, err := RetargetDotenv(sw.Path, sw.Old, sw.New); err != nil {
			t.Fatalf("%s: %v", sw.Path, err)
		}
	}
	if got := read(t, filepath.Join(siteDir, ".env")); got != "SESSION_DOMAIN=acme.loc\n" {
		t.Errorf("site .env: %q", got)
	}
}

// TestTLDHostSwapsSiteAware: a global TLD change moves only what inherits — sites without a
// Site.TLD override, their inherited aliases (which ride the site's effective suffix), and
// worktrees of inheriting sites. Sites on their own suffix, their worktrees, and pinned
// aliases never appear; the old side is derived from the arguments, never from c.TLD.
func TestTLDHostSwapsSiteAware(t *testing.T) {
	cfg := &config.Config{
		TLD: "loc", // already set: TLDHostSwaps must derive the old side from oldTLD
		Sites: []config.Site{
			{Name: "plain", Path: "/code/plain", Domains: []config.Domain{
				{Label: "tenant1"},
				{Label: "app-2", Alias: true},
				{Label: "pinned", TLD: "dev2", Alias: true},
			}},
			{Name: "over", Path: "/code/over", TLD: "loc", Domains: []config.Domain{{Label: "i", Alias: true}}},
		},
		Worktrees: []config.WorktreeEntry{
			{Path: "/wt/f-x", Site: "plain", Slug: "f-x"},
			{Path: "/wt/g-y", Site: "over", Slug: "g-y"},
		},
	}
	swaps := TLDHostSwaps(cfg, "test", "loc")
	want := []HostSwap{
		{Path: filepath.Join("/code/plain", ".env"), Old: "plain.test", New: "plain.loc"},
		{Path: filepath.Join("/code/plain", ".env"), Old: "app-2.test", New: "app-2.loc"},
		{Path: filepath.Join("/wt/f-x", ".env"), Old: "f-x.plain.test", New: "f-x.plain.loc"},
	}
	if !slices.Equal(swaps, want) {
		t.Errorf("got %+v\nwant %+v", swaps, want)
	}

	// The alias swap reaches the site's .env: the alias host itself, as a URL host and in a list.
	dir := t.TempDir()
	site := cfg.Sites[0]
	site.Path = dir
	cfg.Sites[0] = site
	env := filepath.Join(dir, ".env")
	write(t, env, "APP_URL=http://app-2.test\nSANCTUM_STATEFUL_DOMAINS=app-2.test,plain.test\nMAIL_FROM=a@pinned.dev2\n")
	for _, sw := range TLDHostSwaps(cfg, "test", "loc") {
		if _, err := RetargetDotenv(sw.Path, sw.Old, sw.New); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, env); got != "APP_URL=http://app-2.loc\nSANCTUM_STATEFUL_DOMAINS=app-2.loc,plain.loc\nMAIL_FROM=a@pinned.dev2\n" {
		t.Errorf("site .env after the swaps:\n%s", got)
	}
}

// TestSwapsForSite: a site-scoped suffix change moves exactly the site named — its primary
// host, its inherited aliases (which ride the site's effective suffix) and its worktrees —
// and nothing of any other site. Pinned aliases (the Q2 rule) never appear, the old side is
// derived from the arguments (the caller may already have stored the override), an unknown
// name and an unchanged suffix list nothing.
func TestSwapsForSiteMovesOnlyTheSiteItNames(t *testing.T) {
	cfg := &config.Config{
		TLD: "test",
		Sites: []config.Site{
			{Name: "demo", Path: "/code/demo", Domains: []config.Domain{
				{Label: "app-2", Alias: true},
				{Label: "demo2", TLD: "loc", Alias: true},
				{Label: "tenant1"},
			}},
			{Name: "shop", Path: "/code/shop", Domains: []config.Domain{{Label: "shop-2", Alias: true}}},
		},
		Worktrees: []config.WorktreeEntry{
			{Path: "/wt/feature", Site: "demo", Slug: "feature"},
			{Path: "/wt/shopfix", Site: "shop", Slug: "shopfix"},
		},
	}
	want := []HostSwap{
		{Path: filepath.Join("/code/demo", ".env"), Old: "demo.test", New: "demo.loc"},
		{Path: filepath.Join("/code/demo", ".env"), Old: "app-2.test", New: "app-2.loc"},
		{Path: filepath.Join("/wt/feature", ".env"), Old: "feature.demo.test", New: "feature.demo.loc"},
	}
	swaps := SwapsForSite(cfg, "demo", "test", "loc")
	if !slices.Equal(swaps, want) {
		t.Errorf("got %+v\nwant %+v", swaps, want)
	}

	// The inherit pass: the override is already stored (the core sets Site.TLD before listing),
	// and the entries swap back.
	cfg.Sites[0].TLD = "loc"
	back := make([]HostSwap, len(want))
	for i, w := range want {
		back[i] = HostSwap{Path: w.Path, Old: w.New, New: w.Old}
	}
	if got := SwapsForSite(cfg, "demo", "loc", "test"); !slices.Equal(got, back) {
		t.Errorf("inherit pass: got %+v\nwant %+v", got, back)
	}

	if got := SwapsForSite(cfg, "nope", "test", "loc"); got != nil {
		t.Errorf("unknown site: got %+v, want nil", got)
	}
	if got := SwapsForSite(cfg, "demo", "loc", "loc"); got != nil {
		t.Errorf("unchanged suffix: got %+v, want nil", got)
	}
}
