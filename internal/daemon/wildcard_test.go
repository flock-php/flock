package daemon

import (
	"context"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
)

func init() {
	// Keep in-process test daemons off port 53, which the real one uses on Windows.
	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	windowsDNSPort = ln.LocalAddr().(*net.UDPAddr).Port
	_ = ln.Close()
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

func TestWildcardCheckReportsWhyItDoesNotWork(t *testing.T) {
	root, _ := testRoot(t, nil)
	client := startDaemon(t, root)

	st := waitStatus(t, client, func(st api.Status) bool { return st.DNS.Wildcard || st.DNS.WildcardProblem != "" })
	if runtime.GOOS == "windows" {
		// The test daemon's DNS server isn't on port 53, so Windows can never send lookups to it.
		if st.DNS.Wildcard || st.DNS.WildcardProblem == "" {
			t.Fatalf("want a problem, got %+v", st.DNS)
		}
		return
	}
	if !st.DNS.Wildcard {
		t.Fatalf("macOS: want wildcard DNS while the server runs, got %+v", st.DNS)
	}
}
