package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/certs"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestServesEveryDepthAliasAndCatchAllOverTrustedHTTPS runs the embedded Caddy and makes real
// TLS handshakes, verifying the certificates against the Flock CA alone.
func TestServesEveryDepthAliasAndCatchAllOverTrustedHTTPS(t *testing.T) {
	// Not t.TempDir: Caddy keeps its log open after Stop, and Windows can't delete open files.
	dir, err := os.MkdirTemp("", "flock-web-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// php_fastcgi only proxies when the front controller exists; without it file_server answers 404.
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ca, err := certs.Ensure(filepath.Join(dir, "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, err := ca.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(ca.CertPath())
	pool.AppendCertsFromPEM(caPEM)

	// No PHP runs here: a routed request answers 502 from php_fastcgi, the catch-all 404.
	leaves := certs.NewLeaves()
	issued := map[string]int{}
	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   freePort(t),
		HTTPSPort:  freePort(t),
		Bind:       "127.0.0.1",
		TLD:        "test",
		Issue: func(name string) (*tls.Certificate, error) {
			issued[name]++
			return leaves.Get(ca, name)
		},
		Sites: []Site{
			{Domain: "app.test", Aliases: []string{"app-2.test"}, Root: dir, FPM: []string{"127.0.0.1:1"}, CertFile: certFile, KeyFile: keyFile},
			{Domain: "plain.test", Root: dir, FPM: []string{"127.0.0.1:1"}},
		},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	// Every name dials 127.0.0.1; TLS still verifies the requested name.
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, _ := net.SplitHostPort(addr)
				return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	get := func(url string) (*http.Response, string, error) {
		resp, err := client.Get(url)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body), nil
	}
	https := func(host string) string { return fmt.Sprintf("https://%s:%d/", host, cfg.HTTPSPort) }

	// The site answers (with a 502, as nothing runs PHP here) on every depth and alias, and the
	// certificate verifies against the Flock CA alone.
	for _, host := range []string{"app.test", "t1.app.test", "a.b.app.test", "a.b.c.app.test", "a.b.c.d.app.test",
		"app-2.test", "x.app-2.test", "a.b.c.d.app-2.test"} {
		resp, body, err := get(https(host))
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		if resp.StatusCode != http.StatusBadGateway || strings.Contains(body, "No secured Flock site") {
			t.Errorf("%s: want the site (502 from its missing PHP), got %d %q", host, resp.StatusCode, body)
		}
		if got := resp.TLS.PeerCertificates[0]; got.VerifyHostname(host) != nil {
			t.Errorf("%s: served a certificate for %v", host, got.DNSNames)
		}
	}

	// The site's own certificate file covers app.test and *.app.test; only deeper names are issued.
	if issued["app.test"] != 0 || issued["t1.app.test"] != 0 || issued["a.b.app.test"] != 1 || issued["app-2.test"] != 1 {
		t.Errorf("unexpected issued certificates: %v", issued)
	}

	// Five levels is past MaxDepth, an unsecured site and an unknown name: the catch-all answers.
	for _, host := range []string{"a.b.c.d.e.app.test", "plain.test", "nope.test"} {
		resp, body, err := get(https(host))
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "No secured Flock site is linked for "+host) {
			t.Errorf("%s: want the catch-all, got %d %q", host, resp.StatusCode, body)
		}
	}

	// Plain HTTP redirects secured sites at every depth.
	resp, _, err := get(fmt.Sprintf("http://a.b.c.d.app.test:%d/x", cfg.HTTPPort))
	if err != nil || resp.StatusCode != http.StatusPermanentRedirect || !strings.HasPrefix(resp.Header.Get("Location"), "https://a.b.c.d.app.test:") {
		t.Errorf("http redirect: %v %+v", err, resp)
	}

	// A name outside the TLD gets no certificate, and fails fast rather than hanging.
	start := time.Now()
	if _, _, err := get(https("example.com")); err == nil {
		t.Error("example.com: want a TLS failure")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("example.com: the handshake took %v", time.Since(start))
	}
}
