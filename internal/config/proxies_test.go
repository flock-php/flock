package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNormalizeUpstreamAcceptsPortsHostsAndURLs(t *testing.T) {
	for in, want := range map[string]string{
		"3000":                    "http://127.0.0.1:3000",
		" 5173 ":                  "http://127.0.0.1:5173",
		"+3000":                   "http://127.0.0.1:3000",
		"03000":                   "http://127.0.0.1:3000",
		"localhost:5173":          "http://localhost:5173",
		"127.0.0.1:9000":          "http://127.0.0.1:9000",
		"http://127.0.0.1:3000/":  "http://127.0.0.1:3000",
		"https://127.0.0.1:8443":  "https://127.0.0.1:8443",
		"HTTP://LocalHost:3000":   "http://localhost:3000",
		"[::1]:3000":              "http://[::1]:3000",
		"http://dev.internal":     "http://dev.internal",
		"http://192.168.1.20:808": "http://192.168.1.20:808",
	} {
		got, err := NormalizeUpstream(in)
		if err != nil || got != want {
			t.Errorf("NormalizeUpstream(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestNormalizeUpstreamRejectsWhatCantBeProxied(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "empty",
		"0":                          "not a port",
		"70000":                      "not a port",
		"ftp://127.0.0.1:21":         "only http and https",
		"http://127.0.0.1:3000/app":  "only the scheme, host and port",
		"http://127.0.0.1:3000/?a=1": "only the scheme, host and port",
		"http://user@127.0.0.1:3000": "only the scheme, host and port",
		"http://:3000":               "no host",
		"127.0.0.1:99999":            "not a port",
	} {
		if _, err := NormalizeUpstream(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("NormalizeUpstream(%q) = %v; want an error with %q", in, err, want)
		}
	}
}

func TestProxiesDontShareNamesWithSites(t *testing.T) {
	cfg := domainsConfig()

	if err := cfg.UpsertProxy(Proxy{Name: "softpack", Upstream: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.UpsertProxy(Proxy{Name: "softpack", Upstream: "http://127.0.0.1:3001", Secured: true}); err != nil {
		t.Fatal(err)
	}
	if p, ok := cfg.FindProxy("softpack"); !ok || p.Upstream != "http://127.0.0.1:3001" || !p.Secured || len(cfg.Proxies) != 1 {
		t.Fatalf("proxy not replaced: %+v", cfg.Proxies)
	}

	for name, want := range map[string]string{
		"app":    "a linked site's name",
		"app-2":  "already served by app",
		"store":  "already served by shop",
		"My App": "not a valid name",
		"":       "not a valid name",
	} {
		if err := cfg.UpsertProxy(Proxy{Name: name, Upstream: "http://127.0.0.1:3000"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("UpsertProxy(%q) = %v; want an error with %q", name, err, want)
		}
	}

	if err := cfg.Upsert(Site{Name: "softpack", Path: "/code/softpack"}); err == nil || !strings.Contains(err.Error(), "flock unproxy softpack") {
		t.Errorf("linking a proxy's name: %v", err)
	}
	app, _ := cfg.FindSite("app")
	if _, err := cfg.NormalizeDomain(app, "softpack.test"); err == nil || !strings.Contains(err.Error(), "already served by softpack") {
		t.Errorf("an alias with a proxy's domain: %v", err)
	}

	if !cfg.RemoveProxy("softpack") || cfg.RemoveProxy("softpack") || len(cfg.Proxies) != 0 {
		t.Fatalf("remove: %+v", cfg.Proxies)
	}
}

func TestAProxyServesAParkedDirectoryOfTheSameName(t *testing.T) {
	parked := t.TempDir()
	for _, dir := range []string{"softpack", "blog"} {
		if err := os.Mkdir(filepath.Join(parked, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Default()
	cfg.Parked = []string{parked}

	if err := cfg.UpsertProxy(Proxy{Name: "softpack", Upstream: "http://127.0.0.1:3000"}); err != nil {
		t.Fatalf("a parked directory's name: %v", err)
	}
	if _, ok := cfg.FindSite("softpack"); ok {
		t.Error("the parked directory is still a site")
	}
	if _, ok := cfg.FindSite("blog"); !ok {
		t.Error("the other parked directory isn't a site any more")
	}
	if cfg.RemoveProxy("softpack"); !slices.ContainsFunc(cfg.AllSites(), func(s Site) bool { return s.Name == "softpack" }) {
		t.Error("without the proxy, the parked directory is a site again")
	}
}

func TestProxiesRoundTrip(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	if err := cfg.UpsertProxy(Proxy{Name: "s3", Upstream: "http://127.0.0.1:9000", Secured: true}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := loaded.FindProxy("s3"); !ok || p != cfg.Proxies[0] || loaded.ProxyDomain(p) != "s3.test" {
		t.Fatalf("got %+v", loaded.Proxies)
	}
}
