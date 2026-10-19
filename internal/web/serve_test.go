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

// freePort returns a port that's free for TCP and UDP: Caddy also listens for HTTP/3 over
// UDP, and Windows can reserve a port for UDP that's free for TCP.
func freePort(t *testing.T) int {
	t.Helper()
	for range 20 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		pc, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		ln.Close()
		if err == nil {
			pc.Close()
			return port
		}
	}
	t.Fatal("no port is free for both TCP and UDP")
	return 0
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
		Waiting: []Waiting{
			{Domain: "fresh.test", Name: "fresh", PHP: "8.5", State: WaitingInstalling},
			{Domain: "shop.test", Aliases: []string{"shop-2.test"}, Name: "shop", PHP: "8.4", State: WaitingMissing, File: ".flockrc"},
			{Domain: "legacy.test", Name: "legacy", PHP: "7.3", State: WaitingUnavailable},
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

	// The catch-all answers the rest: an unsecured site (at any depth) asks to be secured, by its
	// own name; five levels is past MaxDepth, so like an unknown name it asks to be linked.
	for host, want := range map[string]string{
		"plain.test":         "flock secure plain<",
		"x.y.plain.test":     `href="flock://secure/plain"`,
		"a.b.c.d.e.app.test": "Not linked",
		"nope.test":          "flock link nope --secure",
	} {
		resp, body, err := get(https(host))
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, want) || !strings.Contains(body, ">"+host+"<") {
			t.Errorf("%s: want the catch-all with %q, got %d %q", host, want, resp.StatusCode, body)
		}
	}

	// Over plain HTTP, an unknown name gets the rendered "not linked" page, never cached.
	resp, body, err := get(fmt.Sprintf("http://nope.test:%d/some/path", cfg.HTTPPort))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "Not linked") || !strings.Contains(body, "flock link nope<") ||
		strings.Contains(body, "{{") || strings.Contains(body, `href="flock://secure`) ||
		resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Flock-Page") != "unlinked" {
		t.Errorf("http catch-all: got %d %v %q", resp.StatusCode, resp.Header, body)
	}

	// A site waiting for its PHP says so on every name and scheme, with what it needs.
	for url, want := range map[string][]string{
		https("fresh.test"): {"Needs PHP", "installing PHP 8.5", "data-wait"},
		fmt.Sprintf("http://x.shop-2.test:%d/", cfg.HTTPPort): {"<strong>shop</strong>", "pinned in <code>.flockrc</code>",
			`href="flock://install/php/8.4"`, "flock php install 8.4<"},
		fmt.Sprintf("http://legacy.test:%d/", cfg.HTTPPort): {"no build of PHP 7.3", "flock isolate php &lt;version&gt;"},
	} {
		resp, body, err := get(url)
		if err != nil {
			t.Errorf("%s: %v", url, err)
			continue
		}
		for _, w := range want {
			if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, w) || strings.Contains(body, "Not linked") {
				t.Errorf("%s: want the PHP page with %q, got %d %q", url, w, resp.StatusCode, body)
			}
		}
	}

	// Plain HTTP redirects secured sites at every depth.
	resp, _, err = get(fmt.Sprintf("http://a.b.c.d.app.test:%d/x", cfg.HTTPPort))
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

// TestSendsReverbRequestsToReverb runs the embedded Caddy in front of a stand-in Reverb server: a
// WebSocket connection over HTTPS and a signed broadcast reach it, the app's own pages under
// /app and /apps don't.
func TestSendsReverbRequestsToReverb(t *testing.T) {
	dir, err := os.MkdirTemp("", "flock-web-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ca, err := certs.Ensure(filepath.Join(dir, "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, err := ca.SiteCert("chat.test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	caPEM, _ := os.ReadFile(ca.CertPath())
	pool.AppendCertsFromPEM(caPEM)

	// Reverb's stand-in echoes what a WebSocket sends, and answers anything else with its path.
	reverb, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(reverb, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			fmt.Fprint(w, "reverb "+r.URL.Path)
			return
		}
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
		_, _ = io.Copy(conn, rw)
	}))
	t.Cleanup(func() { reverb.Close() })

	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   freePort(t),
		HTTPSPort:  freePort(t),
		Bind:       "127.0.0.1",
		TLD:        "test",
		Sites: []Site{{Domain: "chat.test", Root: dir, FPM: []string{"127.0.0.1:1"}, Reverb: reverb.Addr().String(),
			CertFile: certFile, KeyFile: keyFile}},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })
	base := fmt.Sprintf("https://chat.test:%d", cfg.HTTPSPort)
	tlsConfig := &tls.Config{RootCAs: pool, ServerName: "chat.test", NextProtos: []string{"http/1.1"}}

	// wss://chat.test/app/<key>, as pusher-js opens it.
	conn, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.HTTPSPort), tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprint(conn, "GET /app/key?protocol=7&client=js HTTP/1.1\r\nHost: chat.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")
	head := make([]byte, 512)
	n, err := conn.Read(head)
	if err != nil || !strings.HasPrefix(string(head[:n]), "HTTP/1.1 101") {
		t.Fatalf("WebSocket handshake: %v %q", err, head[:n])
	}
	fmt.Fprint(conn, "ping")
	if n, err = io.ReadFull(conn, head[:4]); err != nil || string(head[:n]) != "ping" {
		t.Fatalf("through the WebSocket: %v %q", err, head[:n])
	}

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(addr)
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
		},
		ForceAttemptHTTP2: true,
	}}
	for path, want := range map[string]string{
		"/apps/1/events?auth_key=k&auth_timestamp=1&auth_signature=s": "reverb /apps/1/events", // a broadcast
		"/app/key?protocol=7": "reverb /app/key", // a WebSocket over HTTP/2 has no Upgrade header
		"/app/dashboard":      "",                // the app's pages: PHP, a 502 here
		"/apps/list":          "",
	} {
		resp, err := client.Post(base+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if want == "" && resp.StatusCode != http.StatusBadGateway || want != "" && string(body) != want {
			t.Errorf("%s: got %d %q", path, resp.StatusCode, body)
		}
	}
}
