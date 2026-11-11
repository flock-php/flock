package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

// siteTLDFixture builds a Flock data dir in a temp dir: TLD "test", the linked site "demo"
// inheriting it with an inherited alias "app-2" and a subdomain "tenant1", one stored
// worktree "feature" of demo, and the second linked site "shop" (also inheriting) whose
// files must stay byte-identical through any of demo's moves. Each folder carries a .env
// naming its hosts. The socket path lives inside the temp root, so state.commit's daemon
// reload finds no daemon and takes the "not running" path: changeSiteTLD runs end to end
// without a daemon and without any privileged call (those live behind RunE and syncSuffixes).
func siteTLDFixture(t *testing.T) (*state, string, string, string) {
	t.Helper()
	root := t.TempDir()
	demoDir := filepath.Join(root, "sites", "demo")
	shopDir := filepath.Join(root, "sites", "shop")
	wtDir := filepath.Join(root, "worktrees", "feature")
	for _, dir := range []string{demoDir, shopDir, wtDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	demoEnv := "APP_URL=https://demo.test\nSESSION_DOMAIN=.demo.test\n" +
		"SANCTUM_STATEFUL_DOMAINS=demo.test,app-2.test,localhost:3000\n" +
		"MAIL_FROM=noreply@demo.test\nVITE_API=http://tenant1.demo.test/api\n# keep me\nAPP_NAME=Demo\n"
	shopEnv := "APP_URL=https://shop.test\nSESSION_DOMAIN=.shop.test\n"
	wtEnv := "APP_URL=http://feature.demo.test\nSESSION_DOMAIN=.feature.demo.test\n"
	if err := os.WriteFile(filepath.Join(demoDir, ".env"), []byte(demoEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shopDir, ".env"), []byte(shopEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".env"), []byte(wtEnv), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := withDefaults()
	cfg.TLD = "test"
	cfg.Sites = []config.Site{
		{Name: "demo", Path: demoDir, Domains: []config.Domain{{Label: "app-2", Alias: true}, {Label: "tenant1"}}},
		{Name: "shop", Path: shopDir},
	}
	cfg.Worktrees = []config.WorktreeEntry{{Path: wtDir, Site: "demo", Slug: "feature"}}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	return &state{root: root, cfg: cfg}, demoDir, shopDir, wtDir
}

// TestChangeSiteTLDswitchesOneSite: `flock site tld loc demo` on a site inheriting .test
// stores the override, and retargets exactly demo's .env, its inherited alias hosts, and its
// worktree's .env. Shop (the other site, inheriting) is not touched. The suffix set grows:
// oldActive ["test"] → newActive ["test", "loc"], because shop keeps .test alive.
func TestChangeSiteTLDswitchesOneSite(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteTLDFixture(t)

	oldActive := s.cfg.ActiveSuffixes()
	if want := []string{"test"}; !slices.Equal(oldActive, want) {
		t.Fatalf("oldActive: got %v, want %v", oldActive, want)
	}
	shopBefore, err := os.ReadFile(filepath.Join(shopDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	old, warning, err := s.changeSiteTLD("LOC ", "demo", false, &out)
	if err != nil {
		t.Fatalf("changeSiteTLD: %v\n%s", err, out.String())
	}
	if old != "test" {
		t.Errorf("old effective suffix: got %q, want %q", old, "test")
	}
	if warning != "" {
		t.Errorf("loc carries no warning, got %q", warning)
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	site, ok := reloaded.FindSite("demo")
	if !ok || site.TLD != "loc" {
		t.Fatalf("site demo must carry the override, got %+v", site)
	}
	if other, _ := reloaded.FindSite("shop"); other.TLD != "" {
		t.Errorf("site shop must keep inheriting, got %+v", other)
	}
	if want := []string{"loc", "test"}; !slices.Equal(reloaded.ActiveSuffixes(), want) {
		t.Errorf("newActive: got %v, want %v", reloaded.ActiveSuffixes(), want)
	}

	data, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	demo := string(data)
	for _, want := range []string{
		"APP_URL=https://demo.loc",
		"SESSION_DOMAIN=.demo.loc",
		"SANCTUM_STATEFUL_DOMAINS=demo.loc,app-2.loc,localhost:3000", // the inherited alias moved with the site
		"MAIL_FROM=noreply@demo.loc",
		"VITE_API=http://tenant1.demo.test/api", // a key outside retargetKeys keeps its text (Plan 1's rule)
	} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo .env must hold %q:\n%s", want, demo)
		}
	}
	if !strings.Contains(demo, "# keep me") || !strings.Contains(demo, "APP_NAME=Demo") {
		t.Errorf("demo .env must keep comments and other keys:\n%s", demo)
	}
	// The site's host survives only in the untouched VITE_API line: every retargeted key is
	// fully on .loc.
	if strings.Count(demo, "demo.test") != 1 {
		t.Errorf("only the non-retargeted key may keep a demo.test host:\n%s", demo)
	}

	data, err = os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.demo.loc") ||
		!strings.Contains(wt, "SESSION_DOMAIN=.feature.demo.loc") || strings.Contains(wt, ".test") {
		t.Errorf("worktree .env:\n%s", wt)
	}

	shopAfter, err := os.ReadFile(filepath.Join(shopDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(shopAfter) != string(shopBefore) {
		t.Errorf("shop .env touched:\n%s", shopAfter)
	}

	for _, want := range []string{demoDir, wtDir, "APP_URL"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must mention %q, got:\n%s", want, out.String())
		}
	}
}

// TestChangeSiteTLDrejectsPublicTLDWithoutTouchingAnything: validation runs before any
// mutation — a refused suffix leaves config.json and every .env byte-identical.
func TestChangeSiteTLDrejectsPublicTLDWithoutTouchingAnything(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteTLDFixture(t)
	configBefore, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	envBefore := map[string][]byte{}
	for _, dir := range []string{demoDir, shopDir, wtDir} {
		data, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		envBefore[dir] = data
	}

	var out strings.Builder
	old, _, err := s.changeSiteTLD("dev", "demo", false, &out)
	if err == nil || !strings.Contains(err.Error(), "public TLD") {
		t.Fatalf("a public TLD must be refused by name, got err %v, output:\n%s", err, out.String())
	}
	if old != "" {
		t.Errorf("no change happened, old: got %q", old)
	}
	if s.cfg.Sites[0].TLD != "" {
		t.Errorf("in-memory config touched: demo.TLD %q", s.cfg.Sites[0].TLD)
	}
	configAfter, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Errorf("config.json rewritten:\n%s", configAfter)
	}
	for dir, before := range envBefore {
		data, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(before) {
			t.Errorf("%s/.env rewritten:\n%s", dir, data)
		}
	}
}

// TestChangeSiteTLDinheritSwitchesBackAndKeepsExplicitAliases walks the Q2 rule end to end:
// demo switches to .loc, an explicit alias demo2.loc is added, and switching the site back
// to inherit moves the site, its inherited alias and its worktree hosts back to .test while
// the explicit demo2.loc stays exactly where it was put.
func TestChangeSiteTLDinheritSwitchesBackAndKeepsExplicitAliases(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteTLDFixture(t)

	var out strings.Builder
	if old, _, err := s.changeSiteTLD("loc", "demo", false, &out); err != nil || old != "test" {
		t.Fatalf("first switch: old %q err %v\n%s", old, err, out.String())
	}

	// `flock domain add demo2 --suffix loc` on site demo: an explicit alias, stored pinned,
	// printing its resolved host.
	site, ok := s.cfg.FindSite("demo")
	if !ok {
		t.Fatal("site demo missing")
	}
	extra, err := addDomainWithSuffix(s.cfg, &site, "demo2", "loc")
	if err != nil {
		t.Fatalf("domain add --suffix loc: %v", err)
	}
	if extra.Host != "demo2.loc" {
		t.Errorf("resolved host: got %q, want %q", extra.Host, "demo2.loc")
	}
	if !extra.Alias || extra.Entry != "demo2.loc" {
		t.Errorf("must be an alias entry, got %+v", extra)
	}
	wantEntry := config.Domain{Label: "demo2", TLD: "loc", Alias: true}
	if got := site.Domains[len(site.Domains)-1]; got != wantEntry {
		t.Errorf("stored entry: got %+v, want %+v", got, wantEntry)
	}
	site.Parked = false
	if err := s.cfg.Upsert(site); err != nil {
		t.Fatal(err)
	}
	if err := s.cfg.Save(s.root); err != nil {
		t.Fatal(err)
	}
	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if rs, _ := reloaded.FindSite("demo"); !slices.ContainsFunc(rs.Domains, func(d config.Domain) bool { return d == wantEntry }) {
		t.Errorf("reloaded domains must carry the pinned entry, got %+v", rs.Domains)
	}

	// The alias's host lives in the site's .env: the inherit pass must leave it.
	envPath := filepath.Join(demoDir, ".env")
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	env = append(env, []byte("SANCTUM_STATEFUL_DOMAINS=demo.loc,app-2.loc,demo2.loc\n")...)
	if err := os.WriteFile(envPath, env, 0o644); err != nil {
		t.Fatal(err)
	}

	// --inherit: the override clears, the hosts ride back to .test, demo2.loc stays.
	old, _, err := s.changeSiteTLD("", "demo", true, &out)
	if err != nil {
		t.Fatalf("inherit: %v\n%s", err, out.String())
	}
	if old != "loc" {
		t.Errorf("old effective suffix: got %q, want %q", old, "loc")
	}
	reloaded, err = config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if rs, _ := reloaded.FindSite("demo"); rs.TLD != "" {
		t.Errorf("the override must clear, got %q", rs.TLD)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	demo := string(data)
	for _, want := range []string{
		"APP_URL=https://demo.test",
		"SANCTUM_STATEFUL_DOMAINS=demo.test,app-2.test,demo2.loc", // inherited hosts back, the explicit one kept
		"VITE_API=http://tenant1.demo.test/api",
	} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo .env must hold %q:\n%s", want, demo)
		}
	}
	if strings.Contains(demo, "demo.loc") || strings.Contains(demo, "app-2.loc") {
		t.Errorf("the inherit pass must move the site and its inherited alias back:\n%s", demo)
	}
	// The explicit alias's host is exactly what the pass must keep: with demo2.loc taken out,
	// no .loc host survives in the file.
	if strings.Count(demo, "demo2.loc") != 1 {
		t.Errorf("demo2.loc must appear exactly once (the SANCTUM list line):\n%s", demo)
	}
	if strings.Contains(strings.ReplaceAll(demo, "demo2.loc", ""), ".loc") {
		t.Errorf("only the explicit alias may keep a .loc host:\n%s", demo)
	}

	data, err = os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.demo.test") || strings.Contains(wt, ".loc") {
		t.Errorf("worktree .env:\n%s", wt)
	}

	shop, err := os.ReadFile(filepath.Join(shopDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(shop) != "APP_URL=https://shop.test\nSESSION_DOMAIN=.shop.test\n" {
		t.Errorf("shop .env touched:\n%s", shop)
	}

	// Inheriting again is a no-op: "already", nothing written.
	cfgBefore, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out2 strings.Builder
	old, _, err = s.changeSiteTLD("", "demo", true, &out2)
	if err != nil {
		t.Fatalf("second inherit: %v\n%s", err, out2.String())
	}
	if old != "" {
		t.Errorf("the no-op must report no old suffix, got %q", old)
	}
	if !strings.Contains(out2.String(), "already") {
		t.Errorf("output must say it already inherits, got:\n%s", out2.String())
	}
	cfgAfter, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgAfter) != string(cfgBefore) {
		t.Errorf("config.json rewritten on the no-op")
	}
}

// TestAddDomainWithSuffix covers the flag's forms: a suffix pins the alias, the "inherit"
// keyword stores the inherited marker (an empty Domain.TLD with Alias), and a public suffix
// is refused by ValidateTLD.
func TestAddDomainWithSuffix(t *testing.T) {
	s, _, _, _ := siteTLDFixture(t)
	site, ok := s.cfg.FindSite("demo")
	if !ok {
		t.Fatal("site demo missing")
	}

	extra, err := addDomainWithSuffix(s.cfg, &site, "app-3", "inherit")
	if err != nil {
		t.Fatalf("suffix inherit: %v", err)
	}
	if extra.Host != "app-3.test" {
		t.Errorf("inherited alias rides the site's effective suffix, got %q", extra.Host)
	}
	if got := site.Domains[len(site.Domains)-1]; got != (config.Domain{Label: "app-3", Alias: true}) {
		t.Errorf("inherited marker: got %+v", got)
	}

	if _, err := addDomainWithSuffix(s.cfg, &site, "bad", "com"); err == nil ||
		!strings.Contains(err.Error(), "public TLD") {
		t.Errorf("a public suffix must be refused, got %v", err)
	}
	if _, err := addDomainWithSuffix(s.cfg, &site, "a.b", "loc"); err == nil {
		t.Errorf("--suffix takes a single label, got no error for %q", "a.b")
	}
}

// TestRetiredSuffixes pins the pure set-diff that drives retirement.
func TestRetiredSuffixes(t *testing.T) {
	for _, tc := range []struct{ old, new, want []string }{
		{[]string{"test"}, []string{"loc", "test"}, nil}, // a site override adds, retires nothing
		{[]string{"loc", "test"}, []string{"test"}, []string{"loc"}},
		{[]string{"dev", "loc", "test"}, []string{"dev", "test"}, []string{"loc"}},
		{[]string{"test"}, []string{"test"}, nil},
		{nil, []string{"test"}, nil},
	} {
		if got := retiredSuffixes(tc.old, tc.new); !slices.Equal(got, tc.want) {
			t.Errorf("retiredSuffixes(%v, %v): got %v, want %v", tc.old, tc.new, got, tc.want)
		}
	}
}

// TestDeleteRootDirs removes retired suffixes' root stores under roots/ in a temp dir: the
// global root at the store's own dir (ca.crt, ca.key) and the other suffixes' roots stay, a
// missing suffix is a no-op, and a name that cannot be a directory is refused.
func TestDeleteRootDirs(t *testing.T) {
	dir := t.TempDir()
	for _, suffix := range []string{"loc", "dev2", "evil"} {
		d := filepath.Join(dir, "roots", suffix)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "ca.crt"), []byte("root of "+suffix), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), []byte("global root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.key"), []byte("global key"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := deleteRootDirs(dir, []string{"loc", "gone", "../roots/evil", "test"}); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "roots", "loc")) {
		t.Error("roots/loc must be gone")
	}
	if !fileExists(filepath.Join(dir, "roots", "dev2")) {
		t.Error("roots/dev2 must stay")
	}
	if !fileExists(filepath.Join(dir, "roots", "evil")) {
		t.Error("a traversal name must remove nothing")
	}
	for _, name := range []string{"ca.crt", "ca.key"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != globalRoot(name) {
			t.Errorf("global %s touched: %q, %v", name, data, err)
		}
	}
}

func globalRoot(name string) string {
	if name == "ca.crt" {
		return "global root"
	}
	return "global key"
}
