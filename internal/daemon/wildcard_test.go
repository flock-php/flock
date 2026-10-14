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
	dns.ResolverDir = dir
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
		cfg.Upsert(config.Site{Name: "app", Path: t.TempDir(), Secured: true, Domains: []string{"tenant1", "a.b", "app-2.test", "taken.test"}})
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

	// macOS: no resolver file for the test daemon's port.
	if st.DNS.Wildcard || !strings.Contains(st.DNS.WildcardProblem, "flock install") {
		t.Fatalf("want a missing /etc/resolver problem, got %+v", st.DNS)
	}

	// A resolver file for another port.
	file := dns.ResolverFile(cfg.TLD)
	if err := os.WriteFile(file, []byte(dns.ResolverContent(cfg.Ports.DNS+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(file) })
	reloadUntil(t, client, func(st api.Status) bool {
		return !st.DNS.Wildcard && strings.Contains(st.DNS.WildcardProblem, fmt.Sprintf("port %d", cfg.Ports.DNS))
	})

	// The right file, but lookups don't arrive (a VPN overriding DNS), then they do.
	if err := os.WriteFile(file, []byte(dns.ResolverContent(cfg.Ports.DNS)), 0o644); err != nil {
		t.Fatal(err)
	}
	reloadUntil(t, client, func(st api.Status) bool {
		return !st.DNS.Wildcard && strings.Contains(st.DNS.WildcardProblem, "doesn't send .test lookups")
	})
	arrives.Store(true)
	reloadUntil(t, client, func(st api.Status) bool { return st.DNS.Wildcard && st.DNS.WildcardProblem == "" })
}
