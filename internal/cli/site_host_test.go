package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

// siteHostFixture builds a Flock data dir in a temp dir: TLD "test", the linked site "demo"
// inheriting it with an inherited alias "app-2", a pinned alias "demo2.loc" and one stored
// worktree "feature", plus the second linked site "shop" whose files must stay byte-identical
// through any of demo's host moves. Each folder carries a .env naming its hosts. The socket
// path lives inside the temp root, so state.commit's daemon reload finds no daemon and takes
// the "not running" path: changeSiteHost runs end to end without a daemon and without any
// privileged call (mirrors siteTLDFixture).
func siteHostFixture(t *testing.T) (*state, string, string, string) {
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
		"SANCTUM_STATEFUL_DOMAINS=demo.test,app-2.test,demo2.loc\n" +
		"MAIL_FROM=noreply@demo.test\n# keep me\nAPP_NAME=Demo\n"
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
		{Name: "demo", Path: demoDir, Domains: []config.Domain{
			{Label: "app-2", Alias: true},
			{Label: "demo2", TLD: "loc", Alias: true},
		}},
		{Name: "shop", Path: shopDir},
	}
	cfg.Worktrees = []config.WorktreeEntry{{Path: wtDir, Site: "demo", Slug: "feature"}}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	return &state{root: root, cfg: cfg}, demoDir, shopDir, wtDir
}

// fakeWorktree records a worktree of repo at path the way `git worktree add` does (same
// shape as config's test helper), so AllSites synthesizes the row "<slug>.<parent>" a
// command must refuse to retarget.
func fakeWorktree(t *testing.T, repo, path, id, branch string) {
	t.Helper()
	admin := filepath.Join(repo, ".git", "worktrees", id)
	for _, dir := range []string{admin, path} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(admin, "gitdir"): filepath.Join(path, ".git") + "\n",
		filepath.Join(admin, "HEAD"):   "ref: refs/heads/" + branch + "\n",
		filepath.Join(path, ".git"):    "gitdir: " + admin + "\n",
	}
	for file, content := range files {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot reads config.json and every fixture .env, so a refused mutation can be pinned
// byte-identical. Only valid for paths that must write nothing at all (refusals and
// no-ops) — a successful rename legitimately rewrites config.json.
func snapshot(t *testing.T, s *state, dirs ...string) map[string][]byte {
	t.Helper()
	got := map[string][]byte{}
	env := filepath.Join(s.root, "config.json")
	got[env] = readFile(t, env)
	for _, dir := range dirs {
		env = filepath.Join(dir, ".env")
		got[env] = readFile(t, env)
	}
	return got
}

// envSnapshot reads only the fixture .env files — the bytes a host rename must leave
// untouched for sites it does not concern, whatever it does to config.json.
func envSnapshot(t *testing.T, dirs ...string) map[string][]byte {
	t.Helper()
	got := map[string][]byte{}
	for _, dir := range dirs {
		env := filepath.Join(dir, ".env")
		got[env] = readFile(t, env)
	}
	return got
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertUntouched(t *testing.T, before map[string][]byte) {
	t.Helper()
	for path, data := range before {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if string(after) != string(data) {
			t.Errorf("%s rewritten by the refused mutation:\n%s", path, after)
		}
	}
}

// TestChangeSiteHostRenamesPrimaryAndRetargetsEnvs (pins a and g): `flock site host liga
// demo` stores the Host override and retargets demo's .env (primary, mail sender, the
// primary entry of the Sanctum list) and the worktree's .env (feature.demo.test →
// feature.liga.test) — the old host is gone from both. Aliases are the immovable part: the
// inherited app-2.test and the pinned demo2.loc keep their hosts. Shop's .env is
// byte-identical, and the active-suffix set is unchanged (a rename moves no suffix — the
// proof that the run needs no syncSuffixes and no elevation).
func TestChangeSiteHostRenamesPrimaryAndRetargetsEnvs(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteHostFixture(t)
	activeBefore := s.cfg.ActiveSuffixes()
	shopBefore := envSnapshot(t, shopDir)

	var out strings.Builder
	oldPrimary, newPrimary, changed, err := s.changeSiteHost(" LIGA ", "demo", false, &out)
	if err != nil {
		t.Fatalf("changeSiteHost: %v\n%s", err, out.String())
	}
	if !changed {
		t.Fatal("a real rename must report a change")
	}
	if oldPrimary != "demo.test" || newPrimary != "liga.test" {
		t.Errorf("primaries: got %q → %q, want %q → %q", oldPrimary, newPrimary, "demo.test", "liga.test")
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	site, ok := reloaded.FindSite("demo")
	if !ok || site.Host != "liga" {
		t.Fatalf("site demo must carry the override, got %+v", site)
	}

	data, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	demo := string(data)
	for _, want := range []string{
		"APP_URL=https://liga.test",
		"SESSION_DOMAIN=.liga.test",
		"SANCTUM_STATEFUL_DOMAINS=liga.test,app-2.test,demo2.loc", // the aliases stay: an alias is a name of its own
		"MAIL_FROM=noreply@liga.test",
		"# keep me",
		"APP_NAME=Demo",
	} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo .env must hold %q:\n%s", want, demo)
		}
	}
	if strings.Contains(demo, "demo.test") {
		t.Errorf("the old host must be gone from the retargeted keys:\n%s", demo)
	}

	data, err = os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.liga.test") ||
		!strings.Contains(wt, "SESSION_DOMAIN=.feature.liga.test") || strings.Contains(wt, "demo.test") {
		t.Errorf("worktree .env:\n%s", wt)
	}

	assertUntouched(t, shopBefore)

	if !slices.Equal(activeBefore, reloaded.ActiveSuffixes()) {
		t.Errorf("a host rename must not move the suffix set: before %v, after %v", activeBefore, reloaded.ActiveSuffixes())
	}
	for _, want := range []string{demoDir, wtDir, "APP_URL"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must mention %q, got:\n%s", want, out.String())
		}
	}
}

// TestChangeSiteHostRefusesBadLabelsWithoutTouchingAnything (pins b and c): Host is one DNS
// label — "liga.cms", a trailing dot, edge hyphens and an over-long label are refused by
// name, quoting the input; the empty label without --default is a usage error. Every
// refusal leaves config.json and all three .env files byte-identical.
func TestChangeSiteHostRefusesBadLabelsWithoutTouchingAnything(t *testing.T) {
	for _, raw := range []string{"liga.cms", "liga.", ".liga", "-liga", "liga-", "a.b.c", strings.Repeat("a", 64)} {
		t.Run("label "+raw, func(t *testing.T) {
			s, demoDir, shopDir, wtDir := siteHostFixture(t)
			before := snapshot(t, s, demoDir, shopDir, wtDir)

			var out strings.Builder
			oldPrimary, newPrimary, changed, err := s.changeSiteHost(raw, "demo", false, &out)
			if err == nil {
				t.Fatalf("%q must be refused, output:\n%s", raw, out.String())
			}
			if !strings.Contains(err.Error(), `"`+raw+`"`) {
				t.Errorf("the refusal must quote the input %q, got: %v", raw, err)
			}
			if oldPrimary != "" || newPrimary != "" || changed {
				t.Errorf("no change happened, got %q → %q changed %v", oldPrimary, newPrimary, changed)
			}
			if s.cfg.Sites[0].Host != "" {
				t.Errorf("in-memory config touched: demo.Host %q", s.cfg.Sites[0].Host)
			}
			assertUntouched(t, before)
		})
	}

	s, demoDir, shopDir, wtDir := siteHostFixture(t)
	before := snapshot(t, s, demoDir, shopDir, wtDir)
	var out strings.Builder
	if _, _, _, err := s.changeSiteHost("", "demo", false, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("an empty label without --default must be a usage error, got %v\n%s", err, out.String())
	}
	assertUntouched(t, before)
}

// TestChangeSiteHostCollisionLeavesEverything (pin d): demo can't be renamed onto shop's
// host — the refusal names the taken host and its holder and touches nothing.
func TestChangeSiteHostCollisionLeavesEverything(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteHostFixture(t)
	before := snapshot(t, s, demoDir, shopDir, wtDir)

	var out strings.Builder
	_, _, changed, err := s.changeSiteHost("shop", "demo", false, &out)
	if err == nil {
		t.Fatalf("the collision must be refused, output:\n%s", out.String())
	}
	if changed {
		t.Errorf("a refused rename changes nothing")
	}
	msg := err.Error()
	if !strings.Contains(msg, "shop.test") || !strings.Contains(msg, "site shop") || !strings.Contains(msg, "can't move") {
		t.Errorf("the refusal must name the host, the holder and the refusal, got: %v", err)
	}
	assertUntouched(t, before)
}

// TestChangeSiteHostDefaultRestoresTheFolderName (pin e): --default clears the override and
// the .env files swap back onto the folder-name hosts.
func TestChangeSiteHostDefaultRestoresTheFolderName(t *testing.T) {
	s, demoDir, _, wtDir := siteHostFixture(t)
	var out strings.Builder
	if _, _, _, err := s.changeSiteHost("liga", "demo", false, io.Discard); err != nil {
		t.Fatal(err)
	}

	oldPrimary, newPrimary, changed, err := s.changeSiteHost("", "demo", true, &out)
	if err != nil {
		t.Fatalf("changeSiteHost --default: %v\n%s", err, out.String())
	}
	if !changed || oldPrimary != "liga.test" || newPrimary != "demo.test" {
		t.Fatalf("got %q → %q changed %v, want liga.test → demo.test true", oldPrimary, newPrimary, changed)
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if site, _ := reloaded.FindSite("demo"); site.Host != "" {
		t.Errorf("--default must clear the override, got %q", site.Host)
	}
	data, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	demo := string(data)
	for _, want := range []string{
		"APP_URL=https://demo.test",
		"SANCTUM_STATEFUL_DOMAINS=demo.test,app-2.test,demo2.loc",
	} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo .env must be back to %q:\n%s", want, demo)
		}
	}
	if strings.Contains(demo, "liga") {
		t.Errorf("no liga host may survive the swap back:\n%s", demo)
	}
	data, err = os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.demo.test") || strings.Contains(wt, "liga") {
		t.Errorf("worktree .env:\n%s", wt)
	}
}

// TestChangeSiteHostEqualIsANoop (pin f): asking for the host the site already has (in any
// casing the normalization maps to it) says "already" and writes nothing; --default on a
// site already at its default is the same no-op.
func TestChangeSiteHostEqualIsANoop(t *testing.T) {
	s, demoDir, _, _ := siteHostFixture(t)
	if _, _, _, err := s.changeSiteHost("liga", "demo", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, s, demoDir)

	var out strings.Builder
	_, _, changed, err := s.changeSiteHost("LIGA ", "demo", false, &out)
	if err != nil {
		t.Fatalf("equal rename: %v", err)
	}
	if changed {
		t.Error("the equal rename must not report a change")
	}
	if msg := out.String(); !strings.Contains(msg, "already") || !strings.Contains(msg, "liga.test") {
		t.Errorf("the no-op must say it is already served at liga.test, got:\n%s", msg)
	}
	assertUntouched(t, before)

	out.Reset()
	if _, _, changed, err := s.changeSiteHost("", "shop", true, &out); err != nil || changed {
		t.Fatalf("--default on the default must be a no-op: changed %v err %v", changed, err)
	}
	if msg := out.String(); !strings.Contains(msg, "already") {
		t.Errorf("the default no-op must say already, got:\n%s", msg)
	}
}

// TestChangeSiteHostRefusesAWorktree: a worktree follows its parent — `flock site host` on
// the worktree row is refused, pointing at the parent.
func TestChangeSiteHostRefusesAWorktree(t *testing.T) {
	s, demoDir, _, wtDir := siteHostFixture(t)
	fakeWorktree(t, demoDir, wtDir, "feature", "feature")
	if _, ok := s.cfg.FindSite("feature.demo"); !ok {
		t.Fatal("the fixture's git record must synthesize the worktree row")
	}

	var out strings.Builder
	if _, _, _, err := s.changeSiteHost("liga2", "feature.demo", false, &out); err == nil ||
		!strings.Contains(err.Error(), "git worktree of demo") {
		t.Fatalf("the worktree row must be refused with a pointer to its parent, got: %v\n%s", err, out.String())
	}
}

// TestGlobalTLDSwitchFollowsAHostRename is the pinned two-mutation sequence: rename demo to
// liga (the .env files now name liga.test and feature.liga.test), then move the global
// suffix test→loc. TLDHostSwaps must derive BOTH the primary and the worktree side from the
// Host override (retarget.go :231/:247): the .env files end on liga.loc and
// feature.liga.loc. Deriving the identity Name — the pre-fix bug — names hosts the files no
// longer mention, they stay on liga.test, and the site is served nowhere.
func TestGlobalTLDSwitchFollowsAHostRename(t *testing.T) {
	s, demoDir, _, wtDir := siteHostFixture(t)
	var out strings.Builder
	if _, _, _, err := s.changeSiteHost("liga", "demo", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.changeTLD("loc", &out); err != nil {
		t.Fatalf("global tld switch: %v\n%s", err, out.String())
	}

	data, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	demo := string(data)
	for _, want := range []string{"APP_URL=https://liga.loc", "SANCTUM_STATEFUL_DOMAINS=liga.loc,app-2.loc,demo2.loc"} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo .env must hold %q:\n%s", want, demo)
		}
	}
	if strings.Contains(demo, "liga.test") || strings.Contains(demo, "demo.test") {
		t.Errorf("no old host may survive the global switch:\n%s", demo)
	}

	data, err = os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.liga.loc") ||
		strings.Contains(wt, "liga.test") || strings.Contains(wt, "demo.test") {
		t.Errorf("worktree .env:\n%s", wt)
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TLD != "loc" {
		t.Errorf("global suffix: got %q", reloaded.TLD)
	}
	if site, _ := reloaded.FindSite("demo"); site.Host != "liga" {
		t.Errorf("the override must survive the global switch, got %+v", site)
	}
}

// TestSiteTLDSwitchFollowsAHostRename is the same sequence through the SITE-scoped suffix
// change: after renaming demo to liga, `flock site tld loc demo` must move liga.test→
// liga.loc and feature.liga.test→feature.liga.loc (retarget.go :276/:288 — SwapsForSite
// derives the name part from Host, not from the identity Name it is passed).
func TestSiteTLDSwitchFollowsAHostRename(t *testing.T) {
	s, demoDir, _, wtDir := siteHostFixture(t)
	var out strings.Builder
	if _, _, _, err := s.changeSiteHost("liga", "demo", false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if old, _, err := s.changeSiteTLD("loc", "demo", false, &out); err != nil || old != "test" {
		t.Fatalf("site tld switch: old %q err %v\n%s", old, err, out.String())
	}

	data, err := os.ReadFile(filepath.Join(demoDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	demo := string(data)
	for _, want := range []string{"APP_URL=https://liga.loc", "SANCTUM_STATEFUL_DOMAINS=liga.loc,app-2.loc,demo2.loc"} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo .env must hold %q:\n%s", want, demo)
		}
	}
	if strings.Contains(demo, "liga.test") || strings.Contains(demo, "demo.test") {
		t.Errorf("no old host may survive the site switch:\n%s", demo)
	}

	data, err = os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(data); !strings.Contains(wt, "APP_URL=http://feature.liga.loc") ||
		strings.Contains(wt, "liga.test") || strings.Contains(wt, "demo.test") {
		t.Errorf("worktree .env:\n%s", wt)
	}
}
