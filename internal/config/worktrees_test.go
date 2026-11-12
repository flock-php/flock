package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// addWorktree records a worktree of repo at path the way `git worktree add` does, with HEAD as given.
func addWorktree(t *testing.T, repo, path, id, head string) {
	t.Helper()
	admin := filepath.Join(repo, ".git", "worktrees", id)
	for _, dir := range []string{admin, path} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(admin, "gitdir"): filepath.Join(path, ".git") + "\n",
		filepath.Join(admin, "HEAD"):   head + "\n",
		filepath.Join(path, ".git"):    "gitdir: " + admin + "\n",
	}
	for file, content := range files {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorktreesReadsWhatGitRecorded(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "acme")
	addWorktree(t, repo, filepath.Join(dir, "acme-auth"), "acme-auth", "ref: refs/heads/feature/auth")
	addWorktree(t, repo, filepath.Join(dir, "acme-bisect"), "acme-bisect", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	// Still being created: git starts HEAD as the null id.
	addWorktree(t, repo, filepath.Join(dir, "acme-new"), "acme-new", strings.Repeat("0", 40))
	// Deleted without git: the folder is gone, git's record stays until `git worktree prune`.
	addWorktree(t, repo, filepath.Join(dir, "acme-gone"), "acme-gone", "ref: refs/heads/gone")
	if err := os.RemoveAll(filepath.Join(dir, "acme-gone")); err != nil {
		t.Fatal(err)
	}

	got := Worktrees(repo)
	if len(got) != 2 {
		t.Fatalf("want 2 worktrees, got %+v", got)
	}
	if got[0].Path != filepath.Join(dir, "acme-auth") || got[0].Branch != "feature/auth" {
		t.Errorf("unexpected first worktree: %+v", got[0])
	}
	if got[1].Branch != "" || got[1].ID != "acme-bisect" {
		t.Errorf("a detached worktree has no branch: %+v", got[1])
	}

	// A worktree has no worktrees of its own: its .git is a file.
	if wts := Worktrees(filepath.Join(dir, "acme-auth")); wts != nil {
		t.Errorf("a worktree listed worktrees: %+v", wts)
	}
}

func TestWorktreeSlug(t *testing.T) {
	for branch, want := range map[string]string{
		"feature/auth":          "feature-auth",
		"Fix_Invoices.v2":       "fix-invoices-v2",
		"user/üml@ut--x":        "user-mlut-x",
		"-/release/":            "release",
		strings.Repeat("a", 70): strings.Repeat("a", 63),
	} {
		if got := WorktreeSlug(branch); got != want {
			t.Errorf("WorktreeSlug(%q) = %q, want %q", branch, got, want)
		}
	}
}

func TestAllSitesServesWorktreesUnderTheirSite(t *testing.T) {
	parked := t.TempDir()
	repo := filepath.Join(parked, "acme")
	// Next to the site in the parked folder: served as the worktree, not as acme-auth.test too.
	addWorktree(t, repo, filepath.Join(parked, "acme-auth"), "acme-auth", "ref: refs/heads/feature/auth")
	// Another branch with the same slug gets a number.
	addWorktree(t, repo, filepath.Join(t.TempDir(), "acme-auth-2"), "acme-auth-2", "ref: refs/heads/feature_auth")
	addWorktree(t, repo, filepath.Join(t.TempDir(), "bisect"), "bisect", "4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	// Linked as a site of its own: stays that site.
	linked := filepath.Join(t.TempDir(), "spike")
	addWorktree(t, repo, linked, "spike", "ref: refs/heads/spike")

	cfg := Default()
	cfg.Parked = []string{parked}
	if err := cfg.Upsert(Site{Name: "spike", Path: linked}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Upsert(Site{Name: "acme", Path: repo, PHP: "8.4", Secured: true, Domains: []Domain{sub("tenant1")}}); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, s := range cfg.AllSites() {
		names = append(names, s.Name)
	}
	if got, want := strings.Join(names, " "), "acme bisect.acme feature-auth-2.acme feature-auth.acme spike"; got != want {
		t.Fatalf("sites = %s, want %s", got, want)
	}

	wt, ok := cfg.FindSite("feature-auth.acme")
	if !ok || wt.Parent != "acme" || wt.Branch != "feature/auth" || wt.Path != filepath.Join(parked, "acme-auth") {
		t.Fatalf("unexpected worktree: %+v", wt)
	}
	if cfg.Domain(wt) != "feature-auth.acme.test" || wt.PHP != "8.4" || !wt.Secured || len(wt.Domains) != 0 {
		t.Errorf("a worktree is served under its site with the site's settings, not its domains: %+v", wt)
	}
	if s, ok := cfg.SiteForDir(filepath.Join(parked, "acme-auth", "app")); !ok || s.Name != "feature-auth.acme" {
		t.Errorf("SiteForDir in a worktree = %+v", s)
	}

	wt.PHP = "8.5"
	if err := cfg.Upsert(wt); err == nil || !strings.Contains(err.Error(), "worktree of acme") {
		t.Errorf("storing a worktree's settings must be refused, got %v", err)
	}
}

// TestWorktreeFollowsTheSiteTLDOverride: a worktree of a site on its own suffix is served
// at <slug>.<parent>.<the site's suffix> — the override rides the parent's settings.
func TestWorktreeFollowsTheSiteTLDOverride(t *testing.T) {
	parked := t.TempDir()
	repo := filepath.Join(parked, "acme")
	addWorktree(t, repo, filepath.Join(parked, "acme-f"), "acme-f", "ref: refs/heads/f")

	cfg := Default()
	cfg.Upsert(Site{Name: "acme", Path: repo, TLD: "loc"})
	wt, ok := cfg.FindSite("f.acme")
	if !ok {
		t.Fatal("worktree not served")
	}
	if got := cfg.Domain(wt); got != "f.acme.loc" {
		t.Errorf("worktree domain: %q, want f.acme.loc", got)
	}
}

// TestWorktreeFollowsTheSiteHostOverride: a worktree of a site served under a Host
// override is served at <slug>.<the site's host part>.<suffix> — the override rides with
// the other parent settings — while the row keeps its identity name <slug>.<parent name>.
// Two worktrees whose slugs collide get numbered in the host part too: the identity
// fallback must not leave them served at one host.
func TestWorktreeFollowsTheSiteHostOverride(t *testing.T) {
	parked := t.TempDir()
	repo := filepath.Join(parked, "demo")
	addWorktree(t, repo, filepath.Join(parked, "demo-f"), "demo-f", "ref: refs/heads/f")
	addWorktree(t, repo, filepath.Join(parked, "d1"), "d1", "ref: refs/heads/x/y")
	addWorktree(t, repo, filepath.Join(parked, "d2"), "d2", "ref: refs/heads/x_y")

	cfg := Default()
	cfg.Upsert(Site{Name: "demo", Path: repo, Host: "liga"})

	wt, ok := cfg.FindSite("f.demo")
	if !ok {
		t.Fatal("worktree not served")
	}
	if got := cfg.Domain(wt); got != "f.liga.test" {
		t.Errorf("worktree domain: %q, want f.liga.test", got)
	}
	if wt.Name != "f.demo" {
		t.Errorf("worktree identity: %q, want f.demo", wt.Name)
	}

	first, ok := cfg.FindSite("x-y.demo")
	if !ok {
		t.Fatal("first colliding worktree not served")
	}
	second, ok := cfg.FindSite("x-y-2.demo")
	if !ok {
		t.Fatal("second colliding worktree not served")
	}
	if got := cfg.Domain(first); got != "x-y.liga.test" {
		t.Errorf("colliding worktree domain: %q, want x-y.liga.test", got)
	}
	if got := cfg.Domain(second); got != "x-y-2.liga.test" {
		t.Errorf("numbered worktree domain: %q, want x-y-2.liga.test (must not share a host)", got)
	}

	// A DOTTED parent host (the 2026-11-12 relaxation) rides the same synthesis unchanged:
	// the slug is just another label in front of the parent's name part — the worktree is
	// served at f.cms.liga.test under its identity name f.cms.
	cmsRepo := filepath.Join(parked, "cms")
	addWorktree(t, cmsRepo, filepath.Join(parked, "cms-f"), "cms-f", "ref: refs/heads/f")
	if err := cfg.Upsert(Site{Name: "cms", Path: cmsRepo, Host: "cms.liga"}); err != nil {
		t.Fatal(err)
	}
	cmsWt, ok := cfg.FindSite("f.cms")
	if !ok {
		t.Fatal("worktree of the dotted-host parent not served")
	}
	if got := cfg.Domain(cmsWt); got != "f.cms.liga.test" {
		t.Errorf("dotted-parent worktree domain: %q, want f.cms.liga.test", got)
	}
	if cmsWt.Name != "f.cms" {
		t.Errorf("dotted-parent worktree identity: %q, want f.cms", cmsWt.Name)
	}
}

func TestAWorktreeWinsOverTheSameSubdomainOfItsSite(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "acme")
	addWorktree(t, repo, filepath.Join(dir, "acme-api"), "acme-api", "ref: refs/heads/api")

	cfg := Default()
	if err := cfg.Upsert(Site{Name: "acme", Path: repo, Domains: []Domain{sub("api"), sub("tenant1")}}); err != nil {
		t.Fatal(err)
	}
	sites := cfg.AllSites()
	extras := cfg.DomainsBySite(sites)
	if e := extras["acme"]; len(e) != 2 || !strings.Contains(e[0].Problem, "api.acme") || e[1].Problem != "" {
		t.Errorf("api.acme.test belongs to the worktree: %+v", e)
	}

	acme, _ := cfg.FindSite("acme")
	if _, err := cfg.NormalizeDomain(acme, "api"); err == nil {
		t.Error("adding a worktree's domain as a subdomain must be refused")
	}
}
