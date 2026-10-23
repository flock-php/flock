package web

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

// Share is a site served to a tunnel (`flock share`) on a loopback port of its own: the tunnel
// can only ever reach this one site, whatever Host header a request has, and never Flock's own
// pages, other sites or the dev server.
type Share struct {
	Port   int    // loopback port the tunnel connects to
	Root   string // document root
	Public string // <project>/public, where public/hot and public/build are
	// FPM are the upstreams of the version's normal pool: never Xdebug's, which a visitor could
	// otherwise start a debugging session in, and never FrankenPHP's, which can't take the
	// per-request settings below.
	FPM []string
	// Debug turns the app's debug mode on for visitors (APP_DEBUG=true), whatever .env says.
	Debug bool
}

// shareBlock serves a share. Requests come from cloudflared on loopback, so:
//   - forwarded headers a visitor sent are dropped and set from what Cloudflare's edge vouches for
//     (CF-Connecting-IP), so an app that trusts proxies can't be fed another host or scheme;
//   - PHP sees HTTPS, the visitor's address and APP_DEBUG=false (FastCGI params win over .env),
//     so errors don't show stack traces or environment details; a share that asks for debug mode
//     gets APP_DEBUG=true instead. Debugbar stays off either way (its routes are blocked below);
//   - flock_share blocks dotfiles, public/hot and the dev tools' routes, and swaps the Vite dev
//     server's tags for the build (see sharerewrite.go);
//   - search engines are asked not to index it, and links out don't send the tunnel's URL along.
func shareBlock(s Share) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nhttp://:%d {\n\tbind 127.0.0.1\n\timport flock_site\n", s.Port)
	b.WriteString("\theader {\n\t\tX-Robots-Tag \"noindex, nofollow\"\n\t\tReferrer-Policy same-origin\n\t}\n")
	for _, h := range []string{"X-Forwarded-Port", "X-Forwarded-Prefix", "Forwarded", "X-Original-URL", "X-Rewrite-URL"} {
		fmt.Fprintf(&b, "\trequest_header -%s\n", h)
	}
	// The * matcher keeps a public path starting with / from being read as a path matcher.
	fmt.Fprintf(&b, "\tflock_share * %s\n", quote(s.Public))
	fmt.Fprintf(&b, "\troot * %s\n", quote(s.Root))

	quoted := make([]string, len(s.FPM))
	for i, u := range s.FPM {
		quoted[i] = quote(u)
	}
	b.WriteString("\tphp_fastcgi " + strings.Join(quoted, " ") + " {\n")
	envs := [][2]string{
		{"HTTPS", "on"},
		{"REQUEST_SCHEME", "https"},
		{"SERVER_PORT", "443"},
		{"REMOTE_ADDR", "{http.request.header.CF-Connecting-IP}"},
		{"REMOTE_HOST", "{http.request.header.CF-Connecting-IP}"},
		{"APP_DEBUG", strconv.FormatBool(s.Debug)},
		{"DEBUGBAR_ENABLED", "false"},
		{"FLOCK_SHARED", "1"},
	}
	for _, env := range envs {
		fmt.Fprintf(&b, "\t\tenv %s %s\n", env[0], quote(env[1]))
	}
	// The proxy sets X-Forwarded-* from its own connection (cloudflared's, over plain HTTP);
	// header_up runs after that, so these are what an app that trusts proxies sees.
	for _, h := range [][2]string{
		{"X-Forwarded-Proto", "https"},
		{"X-Forwarded-Host", "{host}"},
		{"X-Forwarded-For", "{http.request.header.CF-Connecting-IP}"},
	} {
		fmt.Fprintf(&b, "\t\theader_up %s %s\n", h[0], quote(h[1]))
	}
	if len(s.FPM) > 1 {
		b.WriteString("\t\tlb_policy least_conn\n\t\tlb_try_duration 30s\n")
	}
	b.WriteString("\t}\n\tfile_server {\n\t\thide .*\n\t}\n}\n")
	return b.String()
}

func init() {
	caddy.RegisterModule(shareHandler{})
	httpcaddyfile.RegisterHandlerDirective("flock_share", parseShareHandler)
	// After encode, so the rewritten page is what gets compressed.
	httpcaddyfile.RegisterDirectiveOrder("flock_share", httpcaddyfile.After, "encode")
}

// shareHandler guards a share and rewrites its pages' Vite tags.
type shareHandler struct {
	Public string `json:"public"`
}

func (shareHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.flock_share",
		New: func() caddy.Module { return new(shareHandler) },
	}
}

func parseShareHandler(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var s shareHandler
	err := s.UnmarshalCaddyfile(h.Dispenser)
	return &s, err
}

var _ caddyfile.Unmarshaler = (*shareHandler)(nil)

// UnmarshalCaddyfile is required of handler modules; parseShareHandler does the work.
func (s *shareHandler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next()
	if !d.NextArg() {
		return d.ArgErr()
	}
	s.Public = d.Val()
	if d.NextArg() {
		return d.ArgErr()
	}
	return nil
}

func (s shareHandler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	if blockedSharePath(r.URL.Path) {
		return caddyhttp.Error(http.StatusNotFound, nil)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return next.ServeHTTP(w, r)
	}
	assets := loadViteAssets(s.Public)
	if assets == nil {
		return next.ServeHTTP(w, r)
	}
	rw := &rewriteWriter{ResponseWriterWrapper: &caddyhttp.ResponseWriterWrapper{ResponseWriter: w}, assets: assets, head: r.Method == http.MethodHead}
	err := next.ServeHTTP(rw, r)
	rw.finish()
	return err
}

// devTools are the first path segments of routes that show more than the app itself: other
// requests and sessions (Telescope, Debugbar, Clockwork, Pulse), queues (Horizon), logs, or code
// execution (old Ignition). In the local environment their gates let everyone in.
var devTools = []string{"telescope", "horizon", "pulse", "_debugbar", "_ignition", "clockwork", "__clockwork", "log-viewer", "_boost"}

// shortName matches a Windows 8.3 name (HTACCE~1), which reaches a dotfile under another name.
var shortName = regexp.MustCompile(`~[0-9]`)

// blockedSharePath reports whether a share answers 404 for a request path (already decoded):
//   - a segment starting with a dot (.env, .git, .htaccess, .well-known) or containing an 8.3 name;
//   - backslashes, colons (NTFS streams) and NUL, and segments ending in a dot or space (Windows
//     ignores them, so "hot." opens hot);
//   - public/hot, which names the dev server;
//   - the dev tools' routes (devTools).
//
// Paths compare case-insensitively: macOS and Windows file systems do.
func blockedSharePath(p string) bool {
	if strings.ContainsAny(p, "\\:\x00") {
		return true
	}
	lower := strings.ToLower(p)
	var segs []string
	for _, seg := range strings.Split(lower, "/") {
		if seg == "" {
			continue
		}
		if strings.HasPrefix(seg, ".") || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") || shortName.MatchString(seg) {
			return true
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return false
	}
	if len(segs) == 1 && segs[0] == "hot" {
		return true
	}
	for _, tool := range devTools {
		if segs[0] == tool {
			return true
		}
	}
	return false
}

// rewriteWriter passes a response through, except a page while the dev server runs: that goes
// through viteAssets.rewriteHTML on its way out, streamed (not buffered) so streamed pages still
// stream.
type rewriteWriter struct {
	*caddyhttp.ResponseWriterWrapper
	assets *viteAssets
	head   bool

	wroteHeader bool
	chunks      chan []byte // to the rewriting goroutine, when rewriting
	done        chan struct{}
}

// rewrites reports whether a response is a page to rewrite: a complete, uncompressed HTML 200.
func rewrites(status int, h http.Header) bool {
	if status != http.StatusOK || h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && mt == "text/html"
}

func (w *rewriteWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if !w.head && rewrites(status, w.Header()) {
		w.Header().Del("Content-Length")
		w.Header().Del("ETag")
		w.chunks, w.done = make(chan []byte, 16), make(chan struct{})
		w.ResponseWriter.WriteHeader(status)
		go w.rewrite()
		return
	}
	if rewrites(status, w.Header()) {
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *rewriteWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.chunks == nil {
		return w.ResponseWriter.Write(p)
	}
	w.chunks <- append([]byte(nil), p...)
	return len(p), nil
}

// ReadFrom keeps io.Copy (which the FastCGI proxy uses) from writing past the rewrite.
func (w *rewriteWriter) ReadFrom(r io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.chunks == nil {
		return io.Copy(w.ResponseWriter, r)
	}
	return io.Copy(writerOnly{w}, r)
}

// writerOnly hides ReadFrom, so io.Copy goes through Write.
type writerOnly struct{ io.Writer }

// Flush is left to the rewriting goroutine while it runs: it flushes whenever it has caught up.
func (w *rewriteWriter) Flush() {
	if w.chunks == nil {
		_ = http.NewResponseController(w.ResponseWriter).Flush()
	}
}

func (w *rewriteWriter) finish() {
	if w.chunks != nil {
		close(w.chunks)
		<-w.done
	}
}

func (w *rewriteWriter) rewrite() {
	defer close(w.done)
	// Tokens are small; buffered, the first write is big enough for encode to compress the page.
	out := bufio.NewWriterSize(w.ResponseWriter, 32<<10)
	flush := func() {
		if out.Flush() == nil {
			_ = http.NewResponseController(w.ResponseWriter).Flush()
		}
	}
	r := &chunkReader{chunks: w.chunks, idle: flush}
	defer func() {
		// Whatever happens, drain the handler's writes so it never blocks.
		for range w.chunks {
		}
	}()
	_ = w.assets.rewriteHTML(out, r)
	_ = out.Flush()
}

// chunkReader reads the chunks a handler writes, calling idle before it waits for more.
type chunkReader struct {
	chunks <-chan []byte
	idle   func()
	cur    []byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for len(r.cur) == 0 {
		select {
		case c, ok := <-r.chunks:
			if !ok {
				return 0, io.EOF
			}
			r.cur = c
			continue
		default:
		}
		r.idle()
		c, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		r.cur = c
	}
	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

var (
	_ caddyhttp.MiddlewareHandler = (*shareHandler)(nil)
	_ http.Flusher                = (*rewriteWriter)(nil)
)
