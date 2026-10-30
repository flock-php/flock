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
	if err := cfg.Upsert(Site{Name: "acme", Path: repo, PHP: "8.4", Secured: true, Domains: []string{"tenant1"}}); err != nil {
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

func TestAWorktreeWinsOverTheSameSubdomainOfItsSite(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "acme")
	addWorktree(t, repo, filepath.Join(dir, "acme-api"), "acme-api", "ref: refs/heads/api")

	cfg := Default()
	if err := cfg.Upsert(Site{Name: "acme", Path: repo, Domains: []string{"api", "tenant1"}}); err != nil {
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
