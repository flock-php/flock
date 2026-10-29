package web

import (
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServesAShareThroughCaddy runs the embedded Caddy with a share in front of a FastCGI server
// (Go's, standing in for PHP) and checks what reaches the app and what reaches the visitor.
func TestServesAShareThroughCaddy(t *testing.T) {
	dir, err := os.MkdirTemp("", "flock-share-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	public := viteProject(t, "http://localhost:5173")
	for name, content := range map[string]string{"index.php": "<?php\n", ".env": "APP_KEY=secret\n", "robots.txt": "ok\n"} {
		if err := os.WriteFile(filepath.Join(public, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// The "app": a page with the dev server's tags, plus what PHP was told.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		_ = fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := fcgi.ProcessEnv(r)
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			fmt.Fprintf(w, `<html><head><script type="module" src="http://localhost:5173/@vite/client"></script>`+
				`<script type="module" src="http://localhost:5173/resources/js/app.js"></script></head><body>`)
			// Go's FastCGI server turns HTTPS and REMOTE_ADDR into r.TLS and r.RemoteAddr.
			fmt.Fprintf(w, "<p>tls=%t remote=%s</p>", r.TLS != nil, r.RemoteAddr)
			for _, k := range []string{"SERVER_PORT", "APP_DEBUG", "FLOCK_SHARED"} {
				fmt.Fprintf(w, "<p>%s=%s</p>", k, env[k])
			}
			// Padding past encode's minimum length, so the page is compressed.
			fmt.Fprintf(w, "<!-- %s -->", strings.Repeat("x", 2048))
			fmt.Fprintf(w, "<p>host=%s xfh=%s xfp=%s xff=%s</p></body></html>",
				r.Host, r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For"))
		}))
	}()

	share := Share{Port: freePort(t), Root: public, Public: public, FPM: []string{ln.Addr().String()}}
	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"),
		LogFile:    filepath.Join(dir, "web.log"),
		HTTPPort:   freePort(t),
		HTTPSPort:  freePort(t),
		Bind:       "127.0.0.1",
		TLD:        "test",
		Shares:     []Share{share},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableCompression: true}}
	get := func(path string, header map[string]string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", share.Port, path), nil)
		req.Host = "quiet-river-lamp.trycloudflare.com"
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body io.Reader = resp.Body
		if resp.Header.Get("Content-Encoding") == "gzip" {
			if body, err = gzip.NewReader(resp.Body); err != nil {
				t.Fatal(err)
			}
		}
		data, _ := io.ReadAll(body)
		return resp, string(data)
	}

	resp, page := get("/", map[string]string{
		"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-Host": "evil.com", "X-Forwarded-Proto": "http",
		"X-Forwarded-For": "10.0.0.1", "Accept-Encoding": "gzip",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, page)
	}
	for _, want := range []string{
		`<script type="module" src="/build/assets/app-1a2b.js"></script>`, "tls=true remote=203.0.113.7:", "SERVER_PORT=443",
		"APP_DEBUG=false", "FLOCK_SHARED=1", "host=quiet-river-lamp.trycloudflare.com xfh=quiet-river-lamp.trycloudflare.com xfp=https xff=203.0.113.7",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "5173") || strings.Contains(page, "evil.com") {
		t.Errorf("page still has the dev server or the forged host:\n%s", page)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" || resp.Header.Get("X-Robots-Tag") != "noindex, nofollow" || resp.Header.Get("Referrer-Policy") != "same-origin" {
		t.Errorf("headers: %v", resp.Header)
	}

	if resp, body := get("/build/assets/app-1a2b.js", nil); resp.StatusCode != 200 {
		t.Errorf("built asset: %d %s", resp.StatusCode, body)
	}
	for _, path := range []string{"/.env", "/hot", "/HOT", "/telescope/requests", "/_debugbar/open", "/build/manifest.json/../../.env", "/%2eenv"} {
		if resp, body := get(path, nil); resp.StatusCode != http.StatusNotFound || strings.Contains(body, "secret") || strings.Contains(body, "5173") {
			t.Errorf("%s: %d %q", path, resp.StatusCode, body)
		}
	}
}
