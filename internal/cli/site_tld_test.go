package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/certs"
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

// TestChangeSiteTLDRefusesSelfCollision: a site that pins alias demo2.loc and follows an
// alias demo2 must not be moved onto .loc: the following alias would land on the pinned
// host, and the site would serve one host twice — `flock domain remove demo2.loc` then
// reports success while the name stays served by the surviving entry. The refusal touches
// nothing: config.json and the .env stay byte-identical.
func TestChangeSiteTLDRefusesSelfCollision(t *testing.T) {
	s, demoDir, _, _ := siteTLDFixture(t)
	site, ok := s.cfg.FindSite("demo")
	if !ok {
		t.Fatal("fixture lost demo")
	}
	site.Domains = append(site.Domains,
		config.Domain{Label: "demo2", TLD: "loc", Alias: true},
		config.Domain{Label: "demo2", Alias: true})
	if err := s.cfg.Upsert(site); err != nil {
		t.Fatal(err)
	}
	cfgBefore, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	envBefore, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, _, err := s.changeSiteTLD("loc", "demo", false, &out); err == nil {
		t.Fatalf("the move must be refused, output:\n%s", out.String())
	} else if msg := err.Error(); !strings.Contains(msg, "demo2.loc") || !strings.Contains(msg, "claimed twice") {
		t.Errorf("the refusal must name the host and the double claim, got: %v", err)
	}

	cfgAfter, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgAfter) != string(cfgBefore) {
		t.Errorf("config.json rewritten by the refused move")
	}
	envAfter, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(envAfter) != string(envBefore) {
		t.Errorf("the .env was retargeted by the refused move:\n%s", envAfter)
	}
}

// TestChangeSiteTLDInheritOnGlobalPinnedSite: handing back a site pinned to the *global*
// suffix with --inherit changes the stored state (the override clears, the site follows
// again) while its effective suffix stays the same, so the run takes the no-move branch.
// That branch's message must say the site follows the global suffix now: "now pins .
// (it stops following the global suffix)" states the opposite of what was stored.
func TestChangeSiteTLDInheritOnGlobalPinnedSite(t *testing.T) {
	s, _, _, _ := siteTLDFixture(t)
	if _, _, err := s.changeSiteTLD("test", "demo", false, io.Discard); err != nil {
		t.Fatalf("pin demo to .test: %v", err)
	}

	var out strings.Builder
	old, _, err := s.changeSiteTLD("", "demo", true, &out)
	if err != nil {
		t.Fatalf("inherit: %v\n%s", err, out.String())
	}
	if old != "" {
		t.Errorf("the no-move inherit provisions nothing: old suffix reported as %q, want \"\"", old)
	}
	msg := out.String()
	if strings.Contains(msg, "pins") || strings.Contains(msg, "stops following") {
		t.Errorf("inherit must not report pinning, got:\n%s", msg)
	}
	if !strings.Contains(msg, "follows") || !strings.Contains(msg, ".test") {
		t.Errorf("the message must say demo now follows the global .test, got:\n%s", msg)
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range reloaded.Sites {
		if site.Name == "demo" && site.TLD != "" {
			t.Errorf("the inherit must clear the site's override, got TLD %q", site.TLD)
		}
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
	// The plain form too: a trailing dot is refused as input, not trimmed and added.
	if _, err := s.cfg.AddDomain(&site, "sub.domain."); err == nil || !strings.Contains(err.Error(), "leading or trailing dot") {
		t.Errorf("a trailing dot must be refused, got %v", err)
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

// TestSweepStaleRootsRetiresStoresOutsideTheActiveSet pins F1's shared layer. A stale root
// store (planted with a real cert, as the Goal-4 unprivileged retirement leaves it —
// unlink / `flock domain remove` run no sync): the provisioning list excludes it while it
// is on disk (disk ∩ active, not the disk glob), the sweep returns its fingerprint captured
// while the file exists and hands it to the per-OS untrust hook (whose argv layer is pinned
// per OS, TestUntrustRootArgs on macOS), and deletes the store. The active roots, the global
// root and a foreign directory inside roots/ stay exactly as they were. The capture-only
// sweep (Windows: remove:false, no untrust hook) keeps the store for the elevated run.
func TestSweepStaleRootsRetiresStoresOutsideTheActiveSet(t *testing.T) {
	root := t.TempDir()
	certsDir := filepath.Join(root, "certs")
	active := []string{"test", "loc"}
	for _, suffix := range []string{"loc", "stale", "old"} {
		if _, err := certs.Ensure(filepath.Join(certsDir, "roots", suffix), suffix); err != nil {
			t.Fatal(err)
		}
	}
	global, err := certs.Ensure(certsDir, "test") // the global root lives at the store dir
	if err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(certsDir, "roots", "a.b") // not a suffix label: never swept, never listed
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "ca.crt"), []byte("not a Flock root"), 0o644); err != nil {
		t.Fatal(err)
	}

	staleCert := filepath.Join(certsDir, "roots", "stale", "ca.crt")
	oldCert := filepath.Join(certsDir, "roots", "old", "ca.crt")
	staleFP, err := certs.FileFingerprint(staleCert)
	if err != nil {
		t.Fatal(err)
	}
	oldFP, err := certs.FileFingerprint(oldCert)
	if err != nil {
		t.Fatal(err)
	}
	locBefore, err := os.ReadFile(filepath.Join(certsDir, "roots", "loc", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	globalBefore, err := os.ReadFile(global.CertPath())
	if err != nil {
		t.Fatal(err)
	}

	// Provisioning is the intersection: the stale stores are on disk and not in the list.
	want := []string{filepath.Join(certsDir, "roots", "loc", "ca.crt")}
	if got := activeSuffixRootCerts(root, "test", active); !slices.Equal(got, want) {
		t.Fatalf("provisioning list: got %v, want %v", got, want)
	}

	var calls []string
	swept, err := sweepStaleRoots(io.Discard, certsDir, "test", active, sweepOpts{
		untrust: func(_ io.Writer, suffix, certPath, fingerprint string) error {
			calls = append(calls, suffix+"|"+certPath+"|"+fingerprint)
			return nil
		},
		remove: true,
	})
	if err != nil {
		t.Fatalf("sweepStaleRoots: %v", err)
	}
	if len(swept) != 2 || swept[0].suffix != "old" || swept[1].suffix != "stale" {
		t.Fatalf("swept stores, sorted: %+v", swept)
	}
	if swept[0].fingerprint != oldFP || swept[1].fingerprint != staleFP {
		t.Errorf("swept fingerprints: %+v, want %s and %s", swept, oldFP, staleFP)
	}
	wantCalls := []string{
		"old|" + oldCert + "|" + oldFP,
		"stale|" + staleCert + "|" + staleFP,
	}
	if !slices.Equal(calls, wantCalls) {
		t.Errorf("untrust hook calls: got %v, want %v", calls, wantCalls)
	}
	for _, suffix := range []string{"old", "stale"} {
		if fileExists(filepath.Join(certsDir, "roots", suffix)) {
			t.Errorf("roots/%s must be deleted by the sweep", suffix)
		}
	}
	locAfter, err := os.ReadFile(filepath.Join(certsDir, "roots", "loc", "ca.crt"))
	if err != nil || string(locAfter) != string(locBefore) {
		t.Errorf("roots/loc touched: %v", err)
	}
	globalAfter, err := os.ReadFile(global.CertPath())
	if err != nil || string(globalAfter) != string(globalBefore) {
		t.Errorf("the global root touched: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(junk, "ca.crt")); err != nil || string(data) != "not a Flock root" {
		t.Errorf("a directory that cannot name a suffix was touched: %q, %v", data, err)
	}

	// The Windows seam: capture the fingerprint, leave the store for the elevated run.
	if _, err := certs.Ensure(filepath.Join(certsDir, "roots", "retired2"), "retired2"); err != nil {
		t.Fatal(err)
	}
	captured, err := sweepStaleRoots(io.Discard, certsDir, "test", active, sweepOpts{remove: false})
	if err != nil {
		t.Fatalf("capture sweep: %v", err)
	}
	fp, err := certs.FileFingerprint(filepath.Join(certsDir, "roots", "retired2", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0].suffix != "retired2" || captured[0].fingerprint != fp {
		t.Errorf("captured stores: %+v, want retired2 with fingerprint %s", captured, fp)
	}
	if !fileExists(filepath.Join(certsDir, "roots", "retired2")) {
		t.Error("the capture-only sweep must leave the store for the elevated untrust run")
	}
}

// TestSweepRetiresAnAdoptedGlobalStore pins the adoption end to end at the shared layer: a
// site pinned .loc while the global suffix was test (its store at roots/loc), the global
// switched to loc. dirFor maps the suffix to the store dir — the global root is
// certsDir/ca.crt — so the active set's name now collides with its own store directory:
// provisioning must not carry roots/loc/ca.crt beside the global root (one suffix, two
// trusted roots), and the sweep retires the leftover store — untrust with the captured
// fingerprint, directory deleted — leaving the global root intact.
func TestSweepRetiresAnAdoptedGlobalStore(t *testing.T) {
	root := t.TempDir()
	certsDir := filepath.Join(root, "certs")
	global, err := certs.Ensure(certsDir, "loc") // the global root for loc lives at the store dir
	if err != nil {
		t.Fatal(err)
	}
	siteStore, err := certs.Ensure(filepath.Join(certsDir, "roots", "loc"), "loc") // left by the pinned site
	if err != nil {
		t.Fatal(err)
	}
	fp, err := certs.FileFingerprint(siteStore.CertPath())
	if err != nil {
		t.Fatal(err)
	}
	globalBefore, err := os.ReadFile(global.CertPath())
	if err != nil {
		t.Fatal(err)
	}

	active := []string{"loc"}
	if got := activeSuffixRootCerts(root, "loc", active); got != nil {
		t.Errorf("provisioning list: got %v, want none (the global root is passed separately)", got)
	}

	var calls []string
	swept, err := sweepStaleRoots(io.Discard, certsDir, "loc", active, sweepOpts{
		untrust: func(_ io.Writer, suffix, certPath, fingerprint string) error {
			calls = append(calls, suffix+"|"+certPath+"|"+fingerprint)
			return nil
		},
		remove: true,
	})
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(swept) != 1 || swept[0].suffix != "loc" || swept[0].fingerprint != fp {
		t.Fatalf("swept stores: %+v, want loc with fingerprint %s", swept, fp)
	}
	if len(calls) != 1 || calls[0] != "loc|"+siteStore.CertPath()+"|"+fp {
		t.Errorf("untrust hook calls: %v", calls)
	}
	if fileExists(filepath.Join(certsDir, "roots", "loc")) {
		t.Error("the adopted store must be deleted by the sweep")
	}
	globalAfter, err := os.ReadFile(global.CertPath())
	if err != nil || string(globalAfter) != string(globalBefore) {
		t.Errorf("the global root touched: %v", err)
	}
}

// TestChangeSiteTLDunwritableEnvIsReportedAndSkipped pins the report-and-continue semantics
// for a site-scoped .env Flock may not rewrite: chmod 000 on the site's .env, the switch
// stands, the file is named in the output, and the worktree's .env is retargeted after it.
func TestChangeSiteTLDunwritableEnvIsReportedAndSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: chmod 000 does not block the write")
	}
	s, demoDir, _, wtDir := siteTLDFixture(t)
	envPath := filepath.Join(demoDir, ".env")
	if err := os.Chmod(envPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(envPath, 0o644) })

	var out strings.Builder
	old, _, err := s.changeSiteTLD("loc", "demo", false, &out)
	if err != nil {
		t.Fatalf("an unwritable .env must not fail the switch: %v\n%s", err, out.String())
	}
	if old != "test" {
		t.Errorf("old effective suffix: got %q, want %q", old, "test")
	}
	if !strings.Contains(out.String(), "! could not retarget "+envPath) {
		t.Errorf("the unwritable file must be named in the output:\n%s", out.String())
	}
	site, _ := s.cfg.FindSite("demo")
	if site.TLD != "loc" {
		t.Errorf("the switch must stand: %+v", site)
	}
	// The pass kept going after the unwritable file: the worktree's .env is retargeted.
	data, err := os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.demo.loc") {
		t.Errorf("the worktree .env must be retargeted after the skipped file:\n%s", wt)
	}
}

// TestSuffixNewNote pins the guidance line the domain add prints for a suffix new to the
// provisioning set (c): the --suffix form and the bare full-host form land on the same
// line, an already-active suffix and an inherited alias print nothing, and the bare form's
// suffix comes from the entry NormalizeDomain pinned.
func TestSuffixNewNote(t *testing.T) {
	for _, tc := range []struct {
		name      string
		oldActive []string
		suffix    string
		want      string
	}{
		{"bare host.loc, new suffix", []string{"test"}, "loc", "! .loc is new to Flock's setup: run `flock install` to provision its DNS and HTTPS\n"},
		{"--suffix loc, new suffix", nil, "loc", "! .loc is new to Flock's setup: run `flock install` to provision its DNS and HTTPS\n"},
		{"suffix already active", []string{"loc", "test"}, "loc", ""},
		{"inherited alias / subdomain", []string{"test"}, "", ""},
	} {
		var out strings.Builder
		suffixNewNote(&out, tc.oldActive, tc.suffix)
		if got := out.String(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}

	// The bare form: `flock domain add host.loc` on a .test site stores the pinned marker,
	// which is the suffix the note runs on.
	s, _, _, _ := siteTLDFixture(t)
	site, ok := s.cfg.FindSite("demo")
	if !ok {
		t.Fatal("site demo missing")
	}
	extra, err := s.cfg.AddDomain(&site, "host.loc")
	if err != nil {
		t.Fatalf("domain add host.loc: %v", err)
	}
	if extra.Host != "host.loc" {
		t.Fatalf("resolved host: got %q", extra.Host)
	}
	var out strings.Builder
	suffixNewNote(&out, []string{"test"}, site.Domains[len(site.Domains)-1].TLD)
	if !strings.Contains(out.String(), "!.loc is new") && !strings.Contains(out.String(), ".loc is new to Flock's setup") {
		t.Errorf("the bare form must print the install guidance, got %q", out.String())
	}
}
