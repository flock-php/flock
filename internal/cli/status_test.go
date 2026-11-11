package cli

import (
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/share"
)

func TestShareLineNamesEveryState(t *testing.T) {
	ends := time.Date(2026, 10, 19, 18, 42, 0, 0, time.Local)
	for _, tc := range []struct {
		st   api.ShareStatus
		want string
	}{
		{api.ShareStatus{Site: "acme", State: share.Building}, "acme: building the assets"},
		{api.ShareStatus{Site: "acme", State: share.Starting}, "acme: starting the tunnel"},
		{api.ShareStatus{Site: "acme", State: share.Failed, Error: "cloudflared exited"}, "acme: failed: cloudflared exited"},
		{api.ShareStatus{Site: "acme", State: share.Running, URL: "https://a-b.trycloudflare.com", ExpiresAt: &ends},
			"acme → https://a-b.trycloudflare.com (ends 6:42PM)"},
		{api.ShareStatus{Site: "acme", State: share.Running, URL: "https://a-b.trycloudflare.com", ExpiresAt: &ends, Allow: []string{"bob@acme.com", "*@client.com"}},
			"acme → https://a-b.trycloudflare.com (ends 6:42PM, only bob@acme.com, *@client.com)"},
	} {
		if got := shareLine(tc.st); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

// TestDNSStatusLines: one terse dns row per active suffix; the Windows hosts-file wording only
// on Windows; the old single-suffix shape (no list) still prints exactly one row.
func TestDNSStatusLines(t *testing.T) {
	all := api.DNSStatus{TLD: "test", Addr: "127.0.0.1:53535", Wildcard: true, MaxDepth: 3, Suffixes: []api.SuffixStatus{
		{TLD: "loc", Problem: "the resolver file is missing"},
		{TLD: "test", Wildcard: true},
	}}
	lines := dnsStatusLines(all, "darwin")
	if len(lines) != 2 ||
		!strings.HasPrefix(lines[0], "dns       *.loc doesn't resolve to Flock: the resolver file is missing") ||
		!strings.HasPrefix(lines[1], "dns       *.test → 127.0.0.1, any subdomain (server on 127.0.0.1:53535)") {
		t.Fatalf("lines: %q", lines)
	}

	legacy := api.DNSStatus{TLD: "test", Wildcard: false, WildcardProblem: "no resolver file"}
	if lines := dnsStatusLines(legacy, "linux"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "dns       *.test doesn't resolve to Flock: no resolver file") {
		t.Fatalf("legacy shape: %q", lines)
	}

	windows := api.DNSStatus{TLD: "test", Suffixes: []api.SuffixStatus{{TLD: "test", Problem: "the DNS rule is missing"}}}
	if lines := dnsStatusLines(windows, "windows"); len(lines) != 1 ||
		!strings.HasPrefix(lines[0], "dns       site domains and extra domains → 127.0.0.1 (hosts file); other *.test subdomains don't resolve: the DNS rule is missing") {
		t.Fatalf("windows shape: %q", lines)
	}

	if lines := dnsStatusLines(api.DNSStatus{}, "darwin"); len(lines) != 0 {
		t.Fatalf("no suffix: %q", lines)
	}
}
