package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	nethtml "golang.org/x/net/html"
)

// While `npm run dev` runs, public/hot makes Laravel's @vite point the page at the Vite dev server
// (http://localhost:5173/resources/js/app.js). A shared page can't reach that server, and the tunnel
// never proxies it: Vite serves any file of the project and has had many bypasses of its own checks.
// Instead the shared page gets the production build: assetRewriter replaces the dev server's tags
// with the files public/build/manifest.json names, and drops the ones it can't map.
//
// The rewrite never fetches anything and only adds paths to files that exist under public/build,
// which the tunnel serves anyway. Tokens it doesn't change are copied byte for byte (re-serializing
// HTML could change how a browser parses it), and a tag is only touched when it is a real start tag
// whose URL starts with the dev server's exact origin.

// maxTokenSize caps how much of one token the tokenizer buffers; past it the rest of the page is
// copied unchanged.
const maxTokenSize = 4 << 20

// viteAssets is what the rewrite needs to know about a project: the dev server's origin from
// public/hot and the build's manifest. A nil manifest maps nothing, so every dev tag is dropped.
type viteAssets struct {
	origin   string
	manifest map[string]manifestChunk
	build    string // public/build, where the manifest's files must be
}

type manifestChunk struct {
	File    string   `json:"file"`
	CSS     []string `json:"css"`
	Imports []string `json:"imports"`
}

// loadViteAssets reads public/hot and public/build/manifest.json. It returns nil when the dev server
// isn't running (no hot file), so pages pass through unchanged.
func loadViteAssets(public string) *viteAssets {
	data, err := os.ReadFile(filepath.Join(public, "hot"))
	if err != nil {
		return nil
	}
	a := &viteAssets{origin: devOrigin(string(data)), build: filepath.Join(public, "build")}
	if raw, err := os.ReadFile(filepath.Join(a.build, "manifest.json")); err == nil {
		var m map[string]manifestChunk
		if json.Unmarshal(raw, &m) == nil {
			a.manifest = m
		}
	}
	return a
}

// devOrigin is the hot file's URL as scheme://host[:port], or "" when it isn't one (then nothing
// matches it and the page passes through, which only leaves dev tags the visitor can't load).
func devOrigin(hot string) string {
	u, err := url.Parse(strings.TrimSpace(hot))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// devPath is the part of a dev server URL after the origin ("resources/js/app.js"), or "" when the
// URL isn't the dev server's.
func (a *viteAssets) devPath(value string) (string, bool) {
	if a.origin == "" || !strings.HasPrefix(value, a.origin+"/") {
		return "", false
	}
	return strings.TrimPrefix(value, a.origin+"/"), true
}

// built is the URL of a file the manifest names, when it is a plain relative path that exists
// under public/build; "" otherwise.
func (a *viteAssets) built(file string) string {
	if file == "" || strings.ContainsAny(file, `\:`) || strings.HasPrefix(file, "/") {
		return ""
	}
	clean := path.Clean(file)
	if clean != file || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}
	for _, seg := range strings.Split(clean, "/") {
		if strings.HasPrefix(seg, ".") {
			return ""
		}
	}
	info, err := os.Stat(filepath.Join(a.build, filepath.FromSlash(clean)))
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	u := url.URL{Path: "/build/" + clean}
	return u.EscapedPath()
}

// entry maps a dev server path to the built file and the stylesheets it needs (its own and those
// of the chunks it imports, like Laravel's @vite does for a build).
func (a *viteAssets) entry(key string) (file string, css []string, ok bool) {
	chunk, found := a.manifest[key]
	if !found {
		return "", nil, false
	}
	if file = a.built(chunk.File); file == "" {
		return "", nil, false
	}
	seen := map[string]bool{key: true}
	var collect func(c manifestChunk)
	collect = func(c manifestChunk) {
		for _, f := range c.CSS {
			if u := a.built(f); u != "" && !contains(css, u) {
				css = append(css, u)
			}
		}
		for _, imp := range c.Imports {
			if !seen[imp] {
				seen[imp] = true
				if next, ok := a.manifest[imp]; ok {
					collect(next)
				}
			}
		}
	}
	collect(chunk)
	return file, css, true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// attrName is what an attribute name must look like to be written back.
var attrName = regexp.MustCompile(`^[a-zA-Z_:][-a-zA-Z0-9_:.]*$`)

// tag renders a start tag from its name and attributes, with value replacing the attribute named
// key. Attributes with unusual names are dropped rather than written back unescaped.
func tag(name string, attrs []nethtml.Attribute, key, value string) []byte {
	var b bytes.Buffer
	b.WriteString("<" + name)
	for _, at := range attrs {
		if !attrName.MatchString(at.Key) {
			continue
		}
		v := at.Val
		if at.Key == key {
			v = value
		}
		b.WriteString(" " + at.Key + `="` + html.EscapeString(v) + `"`)
	}
	b.WriteString(">")
	return b.Bytes()
}

func stylesheet(href, nonce string) []byte {
	s := `<link rel="stylesheet" href="` + html.EscapeString(href) + `"`
	if nonce != "" {
		s += ` nonce="` + html.EscapeString(nonce) + `"`
	}
	return []byte(s + ">")
}

func attr(attrs []nethtml.Attribute, key string) (string, bool) {
	for _, at := range attrs {
		if at.Key == key {
			return at.Val, true
		}
	}
	return "", false
}

// rewriteHTML copies an HTML page from r to w, replacing the dev server's tags (see viteAssets). On
// any tokenizer error, everything not yet written is copied unchanged.
func (a *viteAssets) rewriteHTML(w io.Writer, r io.Reader) error {
	z := nethtml.NewTokenizer(r)
	z.SetMaxBuf(maxTokenSize)

	// skipping drops everything up to and including the </script> of a dropped script.
	skipping := false
	// pending is an inline <script> start tag held until its text shows whether it is Vite's React
	// refresh preamble.
	var pending []byte

	write := func(b []byte) error {
		_, err := w.Write(b)
		return err
	}

	for {
		tt := z.Next()
		// Raw must be copied before TagName, TagAttr or Text: they change the buffer in place.
		raw := append([]byte(nil), z.Raw()...)

		if tt == nethtml.ErrorToken {
			if !skipping {
				raw = append(pending, raw...)
			}
			if err := write(append(raw, z.Buffered()...)); err != nil {
				return err
			}
			if errors.Is(z.Err(), io.EOF) {
				return nil
			}
			if errors.Is(z.Err(), nethtml.ErrBufferExceeded) {
				_, err := io.Copy(w, r)
				return err
			}
			return z.Err()
		}

		if pending != nil {
			held := pending
			pending = nil
			if tt == nethtml.TextToken && a.origin != "" && bytes.Contains(raw, []byte(a.origin+"/@react-refresh")) {
				skipping = true
				continue
			}
			if err := write(held); err != nil {
				return err
			}
		}

		if skipping {
			if tt == nethtml.EndTagToken {
				if name, _ := z.TagName(); string(name) == "script" {
					skipping = false
				}
			}
			continue
		}

		if tt != nethtml.StartTagToken && tt != nethtml.SelfClosingTagToken {
			if err := write(raw); err != nil {
				return err
			}
			continue
		}

		name, hasAttr := z.TagName()
		if (string(name) != "script" && string(name) != "link") || !hasAttr && string(name) == "link" {
			if err := write(raw); err != nil {
				return err
			}
			continue
		}
		tagName := string(name)
		var attrs []nethtml.Attribute
		for more := hasAttr; more; {
			var k, v []byte
			k, v, more = z.TagAttr()
			attrs = append(attrs, nethtml.Attribute{Key: string(k), Val: string(v)})
		}

		// A self-closing <script/> still opens a script element, as in browsers.
		out, drop := a.rewriteTag(tagName, attrs)
		switch {
		case drop && tagName == "script":
			skipping = true
		case drop:
		case out == nil && tagName == "script":
			if _, hasSrc := attr(attrs, "src"); !hasSrc {
				pending = raw
				continue
			}
			if err := write(raw); err != nil {
				return err
			}
		case out == nil:
			if err := write(raw); err != nil {
				return err
			}
		default:
			if err := write(out); err != nil {
				return err
			}
		}
	}
}

// rewriteTag decides about one <script> or <link> start tag: nil, false keeps it as it is; drop
// removes it (a script with its content); otherwise out replaces it.
func (a *viteAssets) rewriteTag(name string, attrs []nethtml.Attribute) (out []byte, drop bool) {
	key := "src"
	if name == "link" {
		key = "href"
	}
	value, _ := attr(attrs, key)
	dev, isDev := a.devPath(value)
	if !isDev {
		return nil, false
	}
	if name == "script" && (dev == "@vite/client" || dev == "@react-refresh") {
		return nil, true
	}
	file, css, ok := a.entry(dev)
	if !ok {
		return nil, true
	}
	if name == "link" {
		if rel, _ := attr(attrs, "rel"); !strings.EqualFold(strings.TrimSpace(rel), "stylesheet") {
			return nil, true
		}
		return tag(name, attrs, key, file), false
	}
	// The stylesheets go first, with the script's CSP nonce (Laravel gives its build tags one too).
	nonce, _ := attr(attrs, "nonce")
	var b bytes.Buffer
	for _, href := range css {
		b.Write(stylesheet(href, nonce))
	}
	b.Write(tag(name, attrs, key, file))
	return b.Bytes(), false
}
