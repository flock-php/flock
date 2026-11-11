package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/dns"
)

func init() {
	// Keep in-process test daemons off port 53, which the real one uses on Windows.
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	windowsDNSPort = ln.LocalAddr().(*net.UDPAddr).Port
	_ = ln.Close()

	// The real /etc/resolver points at the real daemon, never at a test daemon's port.
	dir, err := os.MkdirTemp("", "flock-resolver-")
	if err != nil {
		panic(err)
	}
	dns.ResolverDir, dns.ResolvedDir = dir, dir

	// Test daemons' CAs stay out of the browsers in the real home.
	trustInBrowsers = func(string) error { return nil }
}

// resolverFile is the file (or drop-in) `flock install` writes to send the TLD to Flock, and its
// content for port.
func resolverFile(tld string, port int) (string, string) {
	if runtime.GOOS == "linux" {
		return dns.ResolvedFile(), dns.ResolvedContent([]string{tld}, port)
	}
	return dns.ResolverFile(tld), dns.ResolverContent(port)
}

func waitStatus(t *testing.T, client *api.Client, ok func(api.Status) bool) api.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := client.Status(context.Background())
		if err == nil && ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("status never matched: %+v, %v", st, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStatusListsExtraDomainsAndTheirProblems(t *testing.T) {
	root, _ := testRoot(t, func(cfg *config.Config) {
		cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), Secured: true, Domains: []config.Domain{{Label: "tenant1"}, {Label: "a.b"}, {Label: "app-2", Alias: true}, {Label: "taken", Alias: true}}})
		cfg.Upsert(config.Site{Name: "taken", Path: t.TempDir()})
	})
	client := startDaemon(t, root)

	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var app api.SiteStatus
	for _, s := range st.Sites {
		if s.Name == "app" {
			app = s
		}
	}
	got := map[string]api.SiteDomain{}
	for _, d := range app.Domains {
		got[d.Entry] = d
	}
	if d := got["tenant1"]; d.Host != "tenant1.app.test" || d.Alias || d.Problem != "" {
		t.Errorf("tenant1: %+v", d)
	}
	if d := got["a.b"]; d.Host != "a.b.app.test" {
		t.Errorf("a.b: %+v", d)
	}
	if d := got["app-2.test"]; !d.Alias || d.Problem != "" {
		t.Errorf("app-2.test: %+v", d)
	}
	if d := got["taken.test"]; !strings.Contains(d.Problem, "taken") || d.URL != "" {
		t.Errorf("taken.test is the taken site's domain: %+v", d)
	}
	if st.DNS.MaxDepth != config.MaxDepth || st.DNS.TLD != "test" {
		t.Errorf("dns status: %+v", st.DNS)
	}
}

// reloadUntil reloads (which runs the wildcard check again) until the status matches: a reload's
// check is skipped while an earlier one still runs.
func reloadUntil(t *testing.T, client *api.Client, ok func(api.Status) bool) {
	t.Helper()
	waitStatus(t, client, func(st api.Status) bool {
		if ok(st) {
			return true
		}
		_, _ = client.Reload(context.Background())
		return false
	})
}

func TestWildcardCheckReportsWhyItDoesNotWork(t *testing.T) {
	// Set before the daemon starts, which calls it from its own goroutines.
	var arrives atomic.Bool
	old := reaches
	reaches = func(*dns.Server, string) bool { return arrives.Load() }
	t.Cleanup(func() { reaches = old })

	root, cfg := testRoot(t, nil)
	client := startDaemon(t, root)

	st := waitStatus(t, client, func(st api.Status) bool { return st.DNS.Wildcard || st.DNS.WildcardProblem != "" })
	if runtime.GOOS == "windows" {
		// Lookups never reach the test daemon's DNS server.
		if st.DNS.Wildcard || st.DNS.WildcardProblem == "" {
			t.Fatalf("want a problem, got %+v", st.DNS)
		}
		return
	}

	// macOS and Linux: no resolver file for the test daemon's port.
	if st.DNS.Wildcard || !strings.Contains(st.DNS.WildcardProblem, "flock install") {
		t.Fatalf("want a missing /etc/resolver problem, got %+v", st.DNS)
	}

	// A resolver file for another port.
	file, content := resolverFile(cfg.TLD, cfg.Ports.DNS+1)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(file) })
	reloadUntil(t, client, func(st api.Status) bool {
		return !st.DNS.Wildcard && strings.Contains(st.DNS.WildcardProblem, fmt.Sprintf("port %d", cfg.Ports.DNS))
	})

	// The right file, but lookups don't arrive (a VPN overriding DNS), then they do.
	_, content = resolverFile(cfg.TLD, cfg.Ports.DNS)
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reloadUntil(t, client, func(st api.Status) bool {
		return !st.DNS.Wildcard && strings.Contains(st.DNS.WildcardProblem, "doesn't send .test lookups")
	})
	arrives.Store(true)
	reloadUntil(t, client, func(st api.Status) bool { return st.DNS.Wildcard && st.DNS.WildcardProblem == "" })
}

// suffixRow finds one suffix's row in the per-suffix list (nil when the list lacks it).
func suffixRow(dns api.DNSStatus, tld string) *api.SuffixStatus {
	for i, s := range dns.Suffixes {
		if s.TLD == tld {
			return &dns.Suffixes[i]
		}
	}
	return nil
}

// TestWildcardCheckReportsEveryActiveSuffix: the check covers the whole active-suffix set. A
// suffix with no resolver setup gets its own problem row naming the repair, while the global
// suffix's legacy fields keep their exact meaning: they describe the global suffix only.
func TestWildcardCheckReportsEveryActiveSuffix(t *testing.T) {
	var arrives atomic.Bool
	old := reaches
	reaches = func(*dns.Server, string) bool { return arrives.Load() }
	t.Cleanup(func() { reaches = old })

	root, cfg := testRoot(t, func(cfg *config.Config) {
		cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), TLD: "loc"})
	})
	if runtime.GOOS == "windows" {
		client := startDaemon(t, root)
		// No NRPT rule exists for either suffix (the query needs PowerShell, never run in tests).
		st := waitStatus(t, client, func(st api.Status) bool { return st.DNS.WildcardProblem != "" })
		if st.DNS.Wildcard {
			t.Fatalf("global row: %+v", st.DNS)
		}
		loc := suffixRow(st.DNS, "loc")
		if loc == nil || loc.Wildcard || loc.Problem == "" {
			t.Fatalf("loc row: %+v", st.DNS)
		}
		return
	}

	// Provision the global suffix only: .loc is active (the site overrides to it) but has no setup.
	provision := func(tlds ...string) {
		if runtime.GOOS == "linux" {
			if err := os.WriteFile(dns.ResolvedFile(), []byte(dns.ResolvedContent(tlds, cfg.Ports.DNS)), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		for _, tld := range tlds {
			if err := os.WriteFile(dns.ResolverFile(tld), []byte(dns.ResolverContent(cfg.Ports.DNS)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	provision(cfg.TLD)
	t.Cleanup(func() {
		for _, tld := range []string{"test", "loc"} {
			os.Remove(dns.ResolverFile(tld))
		}
		os.Remove(dns.ResolvedFile())
	})

	arrives.Store(true)
	client := startDaemon(t, root)

	st := waitStatus(t, client, func(st api.Status) bool {
		row := suffixRow(st.DNS, "loc")
		return row != nil && !row.Wildcard && row.Problem != ""
	})
	if !st.DNS.Wildcard || st.DNS.WildcardProblem != "" {
		t.Errorf("the global row must stay healthy: %+v", st.DNS)
	}
	if row := suffixRow(st.DNS, "loc"); !strings.Contains(row.Problem, "run `flock install`") {
		t.Errorf("the loc row must name the repair: %q", row.Problem)
	}
	if row := suffixRow(st.DNS, "test"); row == nil || !row.Wildcard || row.Problem != "" {
		t.Errorf("the test row must match the legacy fields: %+v, %+v", row, st.DNS)
	}
	if len(st.DNS.Suffixes) != 2 || st.DNS.Suffixes[0].TLD != "loc" || st.DNS.Suffixes[1].TLD != "test" {
		t.Errorf("the list must cover every active suffix, sorted: %+v", st.DNS.Suffixes)
	}

	// One pass fixes every row: provisioning .loc converges the whole map.
	provision("test", "loc")
	reloadUntil(t, client, func(st api.Status) bool {
		return len(st.DNS.Suffixes) == 2 && suffixRow(st.DNS, "loc").Wildcard && suffixRow(st.DNS, "test").Wildcard
	})
}
