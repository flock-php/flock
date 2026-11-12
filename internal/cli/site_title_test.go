package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/config"
)

// TestChangeSiteTitleSetsSanitizedAndLeavesTheDomainAlone (pin h): `flock site title
// "CMS  Liga"` stores the sanitized one-line display name ("CMS Liga"), and the domain the
// site is served at does not move: the .env files stay byte-identical, the effective domain
// is demo.test before and after, and no retarget ever runs. --default clears it again, and
// the cleared config.json carries no "title" key at all (omitempty — a legacy file reads
// and writes byte-stable). Setting the same title again is an "already" no-op.
func TestChangeSiteTitleSetsSanitizedAndLeavesTheDomainAlone(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteHostFixture(t)
	envsBefore := envSnapshot(t, demoDir, shopDir, wtDir)
	site, _ := s.cfg.FindSite("demo")
	domainBefore := s.cfg.Domain(site)

	var out strings.Builder
	title, changed, err := s.changeSiteTitle("CMS  Liga", "demo", false, &out)
	if err != nil {
		t.Fatalf("changeSiteTitle: %v\n%s", err, out.String())
	}
	if !changed || title != "CMS Liga" {
		t.Fatalf("got title %q changed %v, want %q true", title, changed, "CMS Liga")
	}

	reloaded, err := config.Load(s.root)
	if err != nil {
		t.Fatal(err)
	}
	rSite, _ := reloaded.FindSite("demo")
	if rSite.Title != "CMS Liga" {
		t.Errorf("stored title: got %q, want %q", rSite.Title, "CMS Liga")
	}
	if rSite.Host != "" || rSite.TLD != "" {
		t.Errorf("a title must not touch the host or suffix: %+v", rSite)
	}
	if got := reloaded.Domain(rSite); got != domainBefore || got != "demo.test" {
		t.Errorf("the domain moved: got %q, want %q", got, domainBefore)
	}
	assertUntouched(t, envsBefore) // no retarget ever runs: the .env files are not this command's business

	// Equal again: "already", nothing written — config.json included.
	untouched := snapshot(t, s, demoDir, shopDir, wtDir)
	out.Reset()
	if _, changed, err := s.changeSiteTitle("CMS Liga", "demo", false, &out); err != nil || changed {
		t.Fatalf("the equal title must be a no-op: changed %v err %v", changed, err)
	}
	if msg := out.String(); !strings.Contains(msg, "already") || !strings.Contains(msg, "CMS Liga") {
		t.Errorf("the no-op must say it is already shown as \"CMS Liga\", got:\n%s", msg)
	}
	assertUntouched(t, untouched)

	// Whitespace runs — spaces, tabs, the newline of a pasted line — are word separators at
	// the CLI: folded to single spaces, then SanitizeTitle's rules (the pin of "CMS  Liga").
	// Case survives: a title is display text, not a hostname.
	title, changed, err = s.changeSiteTitle("CMS\t\tLIGA\n", "demo", false, io.Discard)
	if err != nil || !changed || title != "CMS LIGA" {
		t.Errorf("whitespace folding: got title %q changed %v err %v, want %q true", title, changed, err, "CMS LIGA")
	}

	// --default: cleared, and the key leaves the file entirely.
	out.Reset()
	if title, changed, err := s.changeSiteTitle("", "demo", true, &out); err != nil || !changed || title != "" {
		t.Fatalf("--default: got title %q changed %v err %v", title, changed, err)
	}
	data, err := os.ReadFile(filepath.Join(s.root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"title"`) {
		t.Errorf("the cleared title must leave config.json (omitempty):\n%s", data)
	}
	if site, _ := s.cfg.FindSite("demo"); site.Title != "" {
		t.Errorf("cleared title in memory: %q", site.Title)
	}

	// --default again: "already", nothing written — and the .envs are still the fixture's.
	untouched = snapshot(t, s, demoDir, shopDir, wtDir)
	out.Reset()
	if _, changed, err := s.changeSiteTitle("", "demo", true, &out); err != nil || changed {
		t.Fatalf("--default on the default must be a no-op: changed %v err %v", changed, err)
	}
	if msg := out.String(); !strings.Contains(msg, "already") {
		t.Errorf("the default no-op must say already, got:\n%s", msg)
	}
	assertUntouched(t, untouched)
	assertUntouched(t, envsBefore)
}

// TestChangeSiteTitleEmptyWordsIsAUsageError pins the symmetry with changeSiteHost: empty
// words (nothing left after the folding) are NOT the clearing case — without --default the
// core refuses them with the usage line, touching nothing. RunE routes the whole empty-words
// input to the show path (`flock site title ""` shows, like `flock site host ""` shows);
// the core check is the back-stop, exactly as for the host label. --default stays the one
// way to hand the title back to the site name.
func TestChangeSiteTitleEmptyWordsIsAUsageError(t *testing.T) {
	s, demoDir, shopDir, wtDir := siteHostFixture(t)
	untouched := snapshot(t, s, demoDir, shopDir, wtDir)
	for _, raw := range []string{"", "   ", "\t\n"} {
		var out strings.Builder
		_, changed, err := s.changeSiteTitle(raw, "demo", false, &out)
		if err == nil || !strings.Contains(err.Error(), "usage: flock site title") {
			t.Errorf("empty words %q: got changed %v err %v, want a usage error", raw, changed, err)
		}
		if changed {
			t.Errorf("empty words %q must change nothing", raw)
		}
	}
	assertUntouched(t, untouched)
}

// TestChangeSiteTitleRefusesAWorktree: like host and tld, a worktree row is served and
// shown through its parent — the title belongs to the parent site.
func TestChangeSiteTitleRefusesAWorktree(t *testing.T) {
	s, demoDir, _, wtDir := siteHostFixture(t)
	fakeWorktree(t, demoDir, wtDir, "feature", "feature")
	if _, ok := s.cfg.FindSite("feature.demo"); !ok {
		t.Fatal("the fixture's git record must synthesize the worktree row")
	}
	if _, _, err := s.changeSiteTitle("My Worktree", "feature.demo", false, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "git worktree of demo") {
		t.Fatalf("the worktree row must be refused with a pointer to its parent, got: %v", err)
	}
}

// TestTitleWordsAndSite pins the [words…] [site] split: the last argument is the site only
// when it names one, and only from the second argument on — a single word is always the
// title (mirroring `flock site tld <suffix>`).
func TestTitleWordsAndSite(t *testing.T) {
	s, _, _, _ := siteHostFixture(t)

	for _, tc := range []struct {
		args     []string
		words    string
		siteName string
	}{
		{args: []string{"My", "Shop"}, words: "My Shop", siteName: ""},
		{args: []string{"My", "Shop", "shop"}, words: "My Shop", siteName: "shop"},
		{args: []string{"shop"}, words: "shop", siteName: ""},
		{args: []string{"CMS Liga"}, words: "CMS Liga", siteName: ""},
		{args: []string{"Shop"}, words: "Shop", siteName: ""}, // FindSite is case-sensitive; "Shop" is not "shop"
	} {
		words, siteName := titleWordsAndSite(s.cfg, tc.args)
		if words != tc.words || siteName != tc.siteName {
			t.Errorf("titleWordsAndSite(%q): got words %q site %q, want %q %q",
				tc.args, words, siteName, tc.words, tc.siteName)
		}
	}
}
