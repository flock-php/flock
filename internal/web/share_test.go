package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// viteProject writes public/hot (when origin isn't "") and a build with app.js importing a chunk
// that has CSS of its own.
func viteProject(t *testing.T, origin string) string {
	t.Helper()
	public := filepath.Join(t.TempDir(), "public")
	files := map[string]string{
		"build/manifest.json": `{
			"resources/js/app.js": {"file": "assets/app-1a2b.js", "isEntry": true, "imports": ["_shared.js"], "css": ["assets/app-3c4d.css"]},
			"_shared.js": {"file": "assets/shared-5e6f.js", "css": ["assets/shared-7a8b.css"]},
			"resources/css/app.css": {"file": "assets/app-9c0d.css", "isEntry": true},
			"resources/js/evil.js": {"file": "../../.env"},
			"resources/js/missing.js": {"file": "assets/missing.js"}
		}`,
		"build/assets/app-1a2b.js":     "",
		"build/assets/shared-5e6f.js":  "",
		"build/assets/app-3c4d.css":    "",
		"build/assets/shared-7a8b.css": "",
		"build/assets/app-9c0d.css":    "",
	}
	if origin != "" {
		files["hot"] = origin + "\n"
	}
	for name, content := range files {
		path := filepath.Join(public, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return public
}

func rewrite(t *testing.T, public, page string) string {
	t.Helper()
	a := loadViteAssets(public)
	if a == nil {
		t.Fatal("no hot file")
	}
	var out bytes.Buffer
	if err := a.rewriteHTML(&out, strings.NewReader(page)); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestRewriteSwapsLaravelDevTagsForTheBuild(t *testing.T) {
	public := viteProject(t, "http://[::1]:5173")
	page := `<!DOCTYPE html><html><head>` +
		`<script type="module" nonce="n1">import RefreshRuntime from 'http://[::1]:5173/@react-refresh'
RefreshRuntime.injectIntoGlobalHook(window)</script>` +
		`<script type="module" src="http://[::1]:5173/@vite/client" nonce="n1"></script>` +
		`<link rel="stylesheet" href="http://[::1]:5173/resources/css/app.css" />` +
		`<script type="module" src="http://[::1]:5173/resources/js/app.js" nonce="n1"></script>` +
		`</head><body><p>Hi</p></body></html>`

	got := rewrite(t, public, page)
	want := `<!DOCTYPE html><html><head>` +
		`<link rel="stylesheet" href="/build/assets/app-9c0d.css">` +
		`<link rel="stylesheet" href="/build/assets/app-3c4d.css" nonce="n1">` +
		`<link rel="stylesheet" href="/build/assets/shared-7a8b.css" nonce="n1">` +
		`<script type="module" src="/build/assets/app-1a2b.js" nonce="n1"></script>` +
		`</head><body><p>Hi</p></body></html>`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRewriteCopiesEverythingElseByteForByte(t *testing.T) {
	public := viteProject(t, "http://localhost:5173")
	// Odd but valid markup: uppercase tags, unquoted attributes, comments, CRLF, entities, a
	// script whose text mentions the dev server, and a textarea with a tag-like string in it.
	page := "<HTML>\r\n<!-- http://localhost:5173/@vite/client -->\r\n<DIV class=x data-a='1'>&amp; &lt;b&gt;</DIV>\r\n" +
		"<script>var s = '<script src=\"http://localhost:5173/@vite/client\"></scr' + 'ipt>';</script>\r\n" +
		"<textarea><script src=\"http://localhost:5173/@vite/client\"></script></textarea>\r\n" +
		"<p>&lt;script src=\"http://localhost:5173/resources/js/app.js\"&gt;</p>\r\n" +
		"<script src=\"/js/own.js\"></script><img src=\"http://localhost:5173/resources/images/logo.png\">\r\n</HTML>"
	if got := rewrite(t, public, page); got != page {
		t.Fatalf("page changed:\n%s", got)
	}
}

func TestRewriteDropsDevTagsItCannotMap(t *testing.T) {
	public := viteProject(t, "https://app.test:5173")
	page := `<head><script type="module" src="https://app.test:5173/resources/js/evil.js"></script>` +
		`<script type="module" src="https://app.test:5173/resources/js/missing.js">console.log(1)</script>` +
		`<script type="module" src="https://app.test:5173/resources/js/unknown.js"/>window.x = 1</script>` +
		`<link rel="modulepreload" href="https://app.test:5173/resources/js/app.js">` +
		`<script src="https://app.test:5173.evil.com/resources/js/app.js"></script>` +
		`</head>`
	got := rewrite(t, public, page)
	want := `<head><script src="https://app.test:5173.evil.com/resources/js/app.js"></script></head>`
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRewriteEscapesAttributesItWritesBack(t *testing.T) {
	public := viteProject(t, "http://localhost:5173")
	page := `<script data-x="&quot;&gt;&lt;img src=x onerror=alert(1)&gt;" bad"name=1 src="http://localhost:5173/resources/js/app.js"></script>`
	got := rewrite(t, public, page)
	if strings.Contains(got, `"><img`) || strings.Contains(got, `bad"name`) {
		t.Fatalf("attribute not escaped or dropped:\n%s", got)
	}
	if !strings.Contains(got, `src="/build/assets/app-1a2b.js"`) {
		t.Fatalf("not rewritten:\n%s", got)
	}
}

func TestRewriteWithoutABuildDropsEveryDevTag(t *testing.T) {
	public := viteProject(t, "http://localhost:5173")
	if err := os.Remove(filepath.Join(public, "build", "manifest.json")); err != nil {
		t.Fatal(err)
	}
	page := `<script type="module" src="http://localhost:5173/@vite/client"></script>` +
		`<script type="module" src="http://localhost:5173/resources/js/app.js"></script><p>ok</p>`
	if got := rewrite(t, public, page); got != `<p>ok</p>` {
		t.Fatalf("got %q", got)
	}
}

func TestRewriteKeepsTheRestOfAPageWithAHugeToken(t *testing.T) {
	public := viteProject(t, "http://localhost:5173")
	huge := strings.Repeat("a", maxTokenSize+10)
	page := `<script src="http://localhost:5173/resources/js/app.js"></script><div title="` + huge + `">x</div><script src="http://localhost:5173/@vite/client"></script>`
	got := rewrite(t, public, page)
	if !strings.HasPrefix(got, `<link`) || !strings.HasSuffix(got, huge+`">x</div><script src="http://localhost:5173/@vite/client"></script>`) {
		t.Fatalf("lost bytes: %d of %d", len(got), len(page))
	}
}

func TestDevOriginOnlyTakesAPlainOrigin(t *testing.T) {
	for hot, want := range map[string]string{
		"http://[::1]:5173\n":       "http://[::1]:5173",
		"https://app.test:5173":     "https://app.test:5173",
		"http://localhost:5173/":    "http://localhost:5173",
		"http://localhost:5173/sub": "",
		"javascript:alert(1)":       "",
		"http://user:pw@host:5173":  "",
		"":                          "",
		"file:///etc/passwd":        "",
		"http://localhost:5173?x=1": "",
	} {
		if got := devOrigin(hot); got != want {
			t.Errorf("devOrigin(%q) = %q, want %q", hot, got, want)
		}
	}
}

func TestShareBlocksDotfilesHotDevToolsAndWindowsAliases(t *testing.T) {
	blocked := []string{
		"/.env", "/.git/config", "/a/.htaccess", "/.well-known/x", "/hot", "/HOT", "/hot/", "/hot.", "/hot ",
		"/index.php.", "/htacce~1", "/x\\..\\.env", "/hot::$DATA", "/a\x00b",
		"/telescope", "/telescope/requests", "/Horizon/api", "/pulse", "/_debugbar/open", "/_ignition/execute-solution",
		"/clockwork/app", "/__clockwork/latest", "/log-viewer", "/_boost/browser-logs",
	}
	for _, p := range blocked {
		if !blockedSharePath(p) {
			t.Errorf("%q is not blocked", p)
		}
	}
	for _, p := range []string{"/", "/index.php", "/build/assets/app-1a2b.js", "/hotels", "/admin/telescope", "/logs", "/users/1", "/a.b/c"} {
		if blockedSharePath(p) {
			t.Errorf("%q is blocked", p)
		}
	}
}

func TestShareHandlerRewritesOnlyPagesWhileTheDevServerRuns(t *testing.T) {
	public := viteProject(t, "http://localhost:5173")
	page := `<script type="module" src="http://localhost:5173/resources/js/app.js"></script>`
	h := shareHandler{Public: public}

	serve := func(path, contentType string, status int, body string) *httptest.ResponseRecorder {
		next := caddyhttp.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Content-Length", "999")
			w.WriteHeader(status)
			// In two parts, the way a proxied response arrives.
			_, _ = io.WriteString(w, body[:10])
			_, _ = io.Copy(w, strings.NewReader(body[10:]))
			return nil
		})
		rec := httptest.NewRecorder()
		if err := h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil), next); err != nil {
			t.Fatal(err)
		}
		return rec
	}

	rec := serve("/", "text/html; charset=UTF-8", 200, page)
	if !strings.Contains(rec.Body.String(), "/build/assets/app-1a2b.js") || rec.Header().Get("Content-Length") != "" {
		t.Fatalf("page not rewritten: %q (Content-Length %q)", rec.Body.String(), rec.Header().Get("Content-Length"))
	}
	if rec := serve("/api", "application/json", 200, `{"x":"`+page+`"}`); !strings.Contains(rec.Body.String(), "localhost:5173") {
		t.Fatalf("JSON rewritten: %q", rec.Body.String())
	}
	if rec := serve("/missing", "text/html", 404, page); !strings.Contains(rec.Body.String(), "localhost:5173") {
		t.Fatalf("error page rewritten: %q", rec.Body.String())
	}

	if err := os.Remove(filepath.Join(public, "hot")); err != nil {
		t.Fatal(err)
	}
	if rec := serve("/", "text/html", 200, page); rec.Body.String() != page {
		t.Fatalf("rewritten without a dev server: %q", rec.Body.String())
	}

	err := h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/.env", nil), caddyhttp.HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		t.Fatal("a blocked path reached the app")
		return nil
	}))
	var herr caddyhttp.HandlerError
	if !asHandlerError(err, &herr) || herr.StatusCode != http.StatusNotFound {
		t.Fatalf("blocked path: %v", err)
	}
}

func asHandlerError(err error, target *caddyhttp.HandlerError) bool {
	he, ok := err.(caddyhttp.HandlerError)
	if ok {
		*target = he
	}
	return ok
}

func TestCaddyfileServesSharesOnLoopbackWithTheirOwnSettings(t *testing.T) {
	cfg := Config{
		StorageDir: "/tmp/caddy", LogFile: "/tmp/web.log", HTTPPort: 80, HTTPSPort: 443,
		Sites:  []Site{{Domain: "app.test", Root: "/srv/app/public", FPM: []string{"unix//tmp/php.sock"}, DebugFPM: []string{"unix//tmp/debug.sock"}, Debugging: true}},
		Shares: []Share{{Port: 41234, Root: "/srv/app/public", Public: "/srv/app/public", FPM: []string{"unix//tmp/php.sock"}}},
	}
	file := Caddyfile(cfg)
	block := file[strings.Index(file, "http://:41234"):]
	for _, want := range []string{
		"bind 127.0.0.1", "import flock_site", "request_header -Forwarded", `header_up X-Forwarded-Proto "https"`, "flock_share * \"/srv/app/public\"",
		`env APP_DEBUG "false"`, `env DEBUGBAR_ENABLED "false"`, `env HTTPS "on"`, `env REMOTE_ADDR "{http.request.header.CF-Connecting-IP}"`, "hide .*",
		`X-Robots-Tag "noindex, nofollow"`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("share block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "debug.sock") || strings.Contains(block, "xdebug") {
		t.Errorf("share block uses the Xdebug pool:\n%s", block)
	}

	adapter := caddyconfig.GetAdapter("caddyfile")
	json, _, err := adapter.Adapt([]byte(file), nil)
	if err != nil {
		t.Fatal(err)
	}
	// flock_share must run inside encode (so what it writes is compressed) and before PHP.
	s := string(json)
	enc, share, fcgi := strings.Index(s, `"handler":"encode"`), strings.Index(s, `"handler":"flock_share"`), strings.LastIndex(s, `"transport":{"env"`)
	if enc < 0 || share < 0 || fcgi < 0 || !(enc < share && share < fcgi) {
		t.Fatalf("handler order: encode %d, flock_share %d, fastcgi %d", enc, share, fcgi)
	}
}

func TestCaddyfileTurnsDebugModeOnWhenAShareAsksForIt(t *testing.T) {
	cfg := Config{
		StorageDir: "/tmp/caddy", LogFile: "/tmp/web.log", HTTPPort: 80, HTTPSPort: 443,
		Shares: []Share{{Port: 41234, Root: "/srv/app/public", Public: "/srv/app/public", FPM: []string{"unix//tmp/php.sock"}, Debug: true}},
	}
	block := Caddyfile(cfg)
	block = block[strings.Index(block, "http://:41234"):]
	if !strings.Contains(block, `env APP_DEBUG "true"`) || strings.Contains(block, `env APP_DEBUG "false"`) {
		t.Errorf("a debug share doesn't turn debug mode on:\n%s", block)
	}
	// The dev tools stay blocked, so Debugbar's toolbar would only break the page.
	if !strings.Contains(block, `env DEBUGBAR_ENABLED "false"`) {
		t.Errorf("a debug share turns Debugbar on:\n%s", block)
	}
}
