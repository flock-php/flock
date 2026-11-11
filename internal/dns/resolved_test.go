package dns

import (
	"os"
	"strings"
	"testing"
)

// The one drop-in routes every active suffix: Domains lists them all, sorted.
func TestResolvedContentListsEverySuffix(t *testing.T) {
	got := ResolvedContent([]string{"test", "loc", "test"}, 53535)
	if !strings.HasPrefix(got, "# Managed by Flock\n") {
		t.Fatalf("the marker must come first: %q", got)
	}
	if !strings.Contains(got, "DNS=127.0.0.1:53535\n") {
		t.Fatalf("no DNS line: %q", got)
	}
	if !strings.Contains(got, "Domains=~loc ~test\n") {
		t.Fatalf("Domains line: %q", got)
	}
	// A drop-in for the set satisfies the per-suffix check of every suffix in it.
	dir := t.TempDir()
	ResolvedDir = dir
	t.Cleanup(func() { ResolvedDir = "/etc/systemd/resolved.conf.d" })
	if err := os.WriteFile(ResolvedFile(), []byte(got), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tld := range []string{"test", "loc"} {
		if p := ResolvedProblem(tld, 53535); p != "" {
			t.Errorf("problem for .%s: %q", tld, p)
		}
	}
	if p := ResolvedProblem("dev", 53535); !strings.Contains(p, "flock install") {
		t.Errorf("a suffix outside the drop-in's Domains must name the fix: %q", p)
	}
	if p := ResolvedProblem("test", 5300); !strings.Contains(p, "port 5300") {
		t.Errorf("the other port: %q", p)
	}
}

func TestResolvedProblemMissingAndForeign(t *testing.T) {
	ResolvedDir = t.TempDir()
	t.Cleanup(func() { ResolvedDir = "/etc/systemd/resolved.conf.d" })

	if p := ResolvedProblem("test", 53535); !strings.Contains(p, "missing") || !strings.Contains(p, "flock install") {
		t.Fatalf("missing drop-in: %q", p)
	}
	if err := os.WriteFile(ResolvedFile(), []byte("[Resolve]\nDNS=8.8.8.8\nDomains=example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ResolvedProblem("test", 53535); !strings.Contains(p, "flock install") {
		t.Fatalf("a foreign drop-in: %q", p)
	}
}
