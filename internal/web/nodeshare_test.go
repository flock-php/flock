package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestServesANodeSharesThroughCaddy runs the embedded Caddy with a Node.js app's share in front of
// a stand-in dev server, and checks what reaches it.
func TestServesANodeSharesThroughCaddy(t *testing.T) {
	dir, err := os.MkdirTemp("", "flock-node-share-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "path=%s host=%s origin=%s xfh=%s xfp=%s xff=%s",
				r.URL.Path, r.Host, r.Header.Get("Origin"), r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-For"))
		}))
	}()

	share := Share{Port: freePort(t), Proxy: ln.Addr().String(), Domain: "app.test", Origin: "https://app.test", Node: "app"}
	cfg := Config{
		StorageDir: filepath.Join(dir, "caddy"), LogFile: filepath.Join(dir, "web.log"),
		HTTPPort: freePort(t), HTTPSPort: freePort(t), Bind: "127.0.0.1", TLD: "test", Shares: []Share{share},
	}
	if err := Apply(cfg); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	t.Cleanup(func() { _ = Stop() })

	client := &http.Client{Timeout: 5 * time.Second}
	get := func(path string, header map[string]string) (int, string) {
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
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}

	// The dev server sees its own name, the site's origin, and the forwarded headers set from Cloudflare's.
	code, body := get("/", map[string]string{
		"Origin": "https://quiet-river-lamp.trycloudflare.com", "CF-Connecting-IP": "203.0.113.7",
		"X-Forwarded-Host": "evil.com", "X-Forwarded-For": "10.0.0.1",
	})
	want := "path=/ host=app.test origin=https://app.test xfh=quiet-river-lamp.trycloudflare.com xfp=https xff=203.0.113.7"
	if code != http.StatusOK || body != want {
		t.Fatalf("got %d %q, want %q", code, body, want)
	}
	// Without an Origin, none is made up.
	if _, body := get("/about", nil); !strings.Contains(body, "origin= ") {
		t.Errorf("an Origin was added: %q", body)
	}

	// The dev servers' own folders and module paths pass; dotfiles don't.
	for _, path := range []string{"/node_modules/.vite/deps/react.js", "/@id/virtual:pwa-register", "/_next/static/chunks/main.js", "/.svelte-kit/generated/root.js"} {
		if code, body := get(path, nil); code != http.StatusOK || !strings.Contains(body, "path="+path) {
			t.Errorf("%s: %d %q", path, code, body)
		}
	}
	for _, path := range []string{"/.env", "/.env.local", "/.git/config", "/src/.npmrc", "/%2eenv"} {
		if code, body := get(path, nil); code != http.StatusNotFound || strings.Contains(body, "path=") {
			t.Errorf("%s: %d %q", path, code, body)
		}
	}
}

func TestBlockedNodeSharePath(t *testing.T) {
	for path, blocked := range map[string]bool{
		"/":                                false,
		"/node_modules/.vite/deps/vue.js":  false,
		"/@fs/C:/Users/me/app/src/main.ts": false,
		"/.nuxt/dist/app.js":               false,
		"/.ENV":                            true,
		"/.git/HEAD":                       true,
		"/a/.ssh/id_rsa":                   true,
		"/x\\y":                            true,
	} {
		if got := blockedNodeSharePath(path); got != blocked {
			t.Errorf("%s: blocked %v, want %v", path, got, blocked)
		}
	}
}
