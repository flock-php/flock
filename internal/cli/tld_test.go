package cli

import (
	"os"
	"path/filepath"
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
	cfg.Sites = []config.Site{{Name: "app-2", Path: siteDir, Domains: []string{"app-2.test", "tenant1", "x.example"}}}
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
	// The old-TLD alias moved; the subdomain label and the foreign alias stayed: SetTLD's rule.
	want := []string{"app-2.loc", "tenant1", "x.example"}
	if got := reloaded.Sites[0].Domains; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("site domains: got %q, want %q", got, want)
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
	if s.cfg.TLD != "test" || s.cfg.Sites[0].Domains[0] != "app-2.test" {
		t.Errorf("in-memory config touched: TLD %q, domains %q", s.cfg.TLD, s.cfg.Sites[0].Domains)
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
