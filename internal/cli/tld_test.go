package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

// tldFixture builds a Flock data dir in a temp dir: TLD "test", one linked site "app-2" whose
// domains mix an old-TLD alias with a subdomain label and a foreign alias, the site's .env
// naming its hosts, and one stored worktree "feature-x" of that site with its own .env. The
// socket path lives inside the temp root, so state.commit's daemon reload finds no daemon and
// takes the "not running" path: changeTLD runs end to end without a daemon and without any
// privileged call (those live behind RunE and the per-OS retireTLD).
func tldFixture(t *testing.T) (*state, string, string) {
	t.Helper()
	root := t.TempDir()
	siteDir := filepath.Join(root, "sites", "app-2")
	wtDir := filepath.Join(root, "worktrees", "feature-x")
	for _, dir := range []string{siteDir, wtDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	siteEnv := "APP_URL=https://app-2.test\nSESSION_DOMAIN=.app-2.test\n" +
		"MAIL_FROM=noreply@app-2.test\n# the app's name\nAPP_NAME=App2\nSANCTUM_STATEFUL_DOMAINS=app-2.test,localhost:3000\n"
	wtEnv := "APP_URL=http://feature-x.app-2.test\nSESSION_DOMAIN=.feature-x.app-2.test\n"
	if err := os.WriteFile(filepath.Join(siteDir, ".env"), []byte(siteEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".env"), []byte(wtEnv), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := withDefaults()
	cfg.TLD = "test"
	// A subdomain label, an inherited alias (its host follows the global suffix: the .env
	// swaps below move it), and a multi-label subdomain ("x.example", served under the site).
	cfg.Sites = []config.Site{{Name: "app-2", Path: siteDir, Domains: []config.Domain{
		{Label: "app-2", Alias: true}, {Label: "tenant1"}, {Label: "x.example"},
	}}}
	cfg.Worktrees = []config.WorktreeEntry{{Path: wtDir, Site: "app-2", Slug: "feature-x"}}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	return &state{root: root, cfg: cfg}, siteDir, wtDir
}

func TestChangeTLDsavesConfigRewritesAliasesAndEnvFiles(t *testing.T) {
	s, siteDir, wtDir := tldFixture(t)

	var out strings.Builder
	old, warning, err := s.changeTLD("LOC ", &out)
	if err != nil {
		t.Fatalf("changeTLD: %v\n%s", err, out.String())
	}
	if old != "test" {
		t.Errorf("old TLD: got %q, want %q", old, "test")
	}
	if warning != "" {
		t.Errorf("loc carries no warning, got %q", warning)
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.TLD != "loc" {
		t.Errorf("config TLD: got %q, want %q", reloaded.TLD, "loc")
	}
	// SetTLD stores the suffix and nothing else (ruling R5): the entries are byte-stable,
	// the inherited alias follows at use time — visible in the .env swaps below.
	want := []config.Domain{{Label: "app-2", Alias: true}, {Label: "tenant1"}, {Label: "x.example"}}
	if got := reloaded.Sites[0].Domains; !slices.Equal(got, want) {
		t.Errorf("site domains: got %+v, want %+v", got, want)
	}

	siteData, err := os.ReadFile(filepath.Join(siteDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	site := string(siteData)
	for _, want := range []string{
		"APP_URL=https://app-2.loc",
		"SESSION_DOMAIN=.app-2.loc",
		"MAIL_FROM=noreply@app-2.loc",
		"SANCTUM_STATEFUL_DOMAINS=app-2.loc,localhost:3000",
	} {
		if !strings.Contains(site, want) {
			t.Errorf("site .env must hold %q:\n%s", want, site)
		}
	}
	// The comment and the untouched keys stay as written.
	if !strings.Contains(site, "# the app's name") || !strings.Contains(site, "APP_NAME=App2") {
		t.Errorf("site .env must keep comments and other keys:\n%s", site)
	}
	if strings.Contains(site, ".test") {
		t.Errorf("site .env must hold no old-TLD host:\n%s", site)
	}

	wtData, err := os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if wt := string(wtData); !strings.Contains(wt, "APP_URL=http://feature-x.app-2.loc") ||
		!strings.Contains(wt, "SESSION_DOMAIN=.feature-x.app-2.loc") || strings.Contains(wt, ".test") {
		t.Errorf("worktree .env:\n%s", wt)
	}

	// The user sees which files were retargeted.
	for _, want := range []string{siteDir, wtDir, "APP_URL"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output must mention %q, got:\n%s", want, out.String())
		}
	}
}

// TestChangeTLDRefusesSelfCollision: the global switch runs the landing probe before
// touching anything. A site's following alias riding onto its own pinned host would make
// the site serve one host twice — `domain remove` then reports success while the name
// stays served — so the switch is refused, and a refused TLD changes nothing: config.json
// and both .env files stay byte-identical.
func TestChangeTLDRefusesSelfCollision(t *testing.T) {
	s, siteDir, wtDir := tldFixture(t)
	site, ok := s.cfg.FindSite("app-2")
	if !ok {
		t.Fatal("fixture lost app-2")
	}
	site.Domains = append(site.Domains,
		config.Domain{Label: "demo2", TLD: "loc", Alias: true},
		config.Domain{Label: "demo2", Alias: true})
	if err := s.cfg.Upsert(site); err != nil {
		t.Fatal(err)
	}
	if err := s.cfg.Save(s.root); err != nil {
		t.Fatal(err)
	}
	cfgBefore, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	siteBefore, err := os.ReadFile(filepath.Join(siteDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	wtBefore, err := os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	old, _, err := s.changeTLD("loc", &out)
	if err == nil {
		t.Fatalf("the move must be refused, output:\n%s", out.String())
	}
	if msg := err.Error(); !strings.Contains(msg, "demo2.loc") || !strings.Contains(msg, "claimed twice") {
		t.Errorf("the refusal must name the host and the double claim, got: %v", err)
	}
	if old != "" {
		t.Errorf("a refused switch reports no old suffix, got %q", old)
	}
	if cfgAfter, _ := os.ReadFile(filepath.Join(s.root, "config.json")); string(cfgAfter) != string(cfgBefore) {
		t.Errorf("config.json rewritten by the refused switch")
	}
	if siteAfter, _ := os.ReadFile(filepath.Join(siteDir, ".env")); string(siteAfter) != string(siteBefore) {
		t.Errorf("the site .env was retargeted by the refused switch")
	}
	if wtAfter, _ := os.ReadFile(filepath.Join(wtDir, ".env")); string(wtAfter) != string(wtBefore) {
		t.Errorf("the worktree .env was retargeted by the refused switch")
	}
}

// TestChangeTLDRefusesProxyCollision: a proxy's derived domain rides on the global suffix,
// so a site's following alias moving onto it is a collision the switch creates: refused,
// nothing touched.
func TestChangeTLDRefusesProxyCollision(t *testing.T) {
	s, _, _ := tldFixture(t)
	if err := s.cfg.Upsert(config.Site{Name: "api", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	s.cfg.Proxies = append(s.cfg.Proxies, config.Proxy{Name: "api", Upstream: "http://127.0.0.1:3000"})
	if err := s.cfg.Save(s.root); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if _, _, err := s.changeTLD("loc", &out); err == nil {
		t.Fatalf("the move must be refused, output:\n%s", out.String())
	} else if msg := err.Error(); !strings.Contains(msg, "api.loc") {
		t.Errorf("the refusal must name api.loc, got: %v", err)
	}
}

func TestChangeTLDrejectsPublicTLDWithoutTouchingAnything(t *testing.T) {
	s, siteDir, wtDir := tldFixture(t)
	configBefore, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	siteBefore, err := os.ReadFile(filepath.Join(siteDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	wtBefore, err := os.ReadFile(filepath.Join(wtDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	old, _, err := s.changeTLD("dev", &out)
	if err == nil || !strings.Contains(err.Error(), "public TLD") {
		t.Fatalf("a public TLD must be refused by name, got err %v, output:\n%s", err, out.String())
	}
	if old != "" {
		t.Errorf("no change happened, old: got %q", old)
	}
	// Validation runs before any mutation: config, aliases and .env files are exactly as they were.
	if s.cfg.TLD != "test" || s.cfg.Sites[0].Domains[0] != (config.Domain{Label: "app-2", Alias: true}) {
		t.Errorf("in-memory config touched: TLD %q, domains %+v", s.cfg.TLD, s.cfg.Sites[0].Domains)
	}
	configAfter, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Errorf("config.json rewritten:\n%s", configAfter)
	}
	for _, tc := range []struct {
		dir    string
		before []byte
	}{{siteDir, siteBefore}, {wtDir, wtBefore}} {
		data, err := os.ReadFile(filepath.Join(tc.dir, ".env"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(tc.before) {
			t.Errorf("%s/.env rewritten:\n%s", tc.dir, data)
		}
	}
}

func TestChangeTLDonTheCurrentTLDsaysAlreadyAndWritesNothing(t *testing.T) {
	s, siteDir, _ := tldFixture(t)
	configBefore, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	siteBefore, err := os.ReadFile(filepath.Join(siteDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}

	// ".test" normalizes to the current "test": equal after normalizing is a no-op.
	var out strings.Builder
	old, _, err := s.changeTLD(".test", &out)
	if err != nil {
		t.Fatalf("changeTLD: %v\n%s", err, out.String())
	}
	if old != "" {
		t.Errorf("the no-op must report no old TLD, got %q", old)
	}
	if !strings.Contains(out.String(), "already") {
		t.Errorf("output must say it is the current TLD, got:\n%s", out.String())
	}
	configAfter, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(configAfter) != string(configBefore) {
		t.Errorf("config.json rewritten:\n%s", configAfter)
	}
	siteAfter, err := os.ReadFile(filepath.Join(siteDir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if string(siteAfter) != string(siteBefore) {
		t.Errorf("site .env rewritten:\n%s", siteAfter)
	}
}
