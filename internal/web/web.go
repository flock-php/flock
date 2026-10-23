// Package web is Flock's HTTP(S) front: an embedded Caddy that routes each
// site's domain (and subdomains) to the site's PHP FastCGI pool.
//
// Caddy runs in-process, so there is no separate web server binary to ship,
// and it works the same on macOS and Windows.
package web

import (
	"crypto/tls"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	_ "github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/caddyserver/caddy/v2/modules/standard"

	"flock/internal/config"
)

// Site is one served application.
type Site struct {
	Domain  string   // e.g. watchtower.test
	Aliases []string // other names served like Domain, e.g. watchtower-2.test
	Root    string   // document root, e.g. <path>/public
	FPM     []string // FastCGI upstreams: "unix/<socket>" (PHP-FPM) or "127.0.0.1:<port>" (php-cgi workers)
	// DebugFPM are the upstreams of the pool that loads Xdebug. Requests that ask for Xdebug go
	// there, and with Debugging (the project has breakpoints) every request does.
	DebugFPM  []string
	Debugging bool
	// Proxy is the loopback address of the site's own FrankenPHP ("127.0.0.1:<port>"). When set, requests
	// go there instead of to FPM; those that ask for Xdebug still go to DebugFPM.
	Proxy string
	// CertFile and KeyFile (from certs.SiteCert, covering Domain and *.Domain) are set when the site
	// is secured. Deeper subdomains and aliases get certificates from Config.Issue.
	CertFile string
	KeyFile  string
}

// Waiting is a linked site that can't be served because its PHP isn't installed: its names get
// Flock's page saying so (and offering the install) instead of the "not linked" page.
type Waiting struct {
	Domain  string
	Aliases []string
	Name    string // the site's name
	PHP     string // the version it needs
	State   string // WaitingInstalling, WaitingMissing or WaitingUnavailable
	File    string // the project file that pins PHP ("" when the version isn't pinned there)
}

// States of a Waiting site: its PHP is being installed (also the first run's default PHP), can
// be installed, or has no build for this platform.
const (
	WaitingInstalling  = "installing"
	WaitingMissing     = "missing"
	WaitingUnavailable = "unavailable"
)

// Rootless is a linked site that isn't served because Flock can't tell its document root, or the
// root has nothing to serve: the root set for it doesn't exist, a Composer project has none of the
// usual ones, or the root has no index.php or index.html. Its names get Flock's page saying so,
// with the folders that could be the root.
type Rootless struct {
	Domain      string
	Aliases     []string
	Name        string   // the site's name
	State       string   // why: "missing", "not_found" or "empty" (see project.DocRoot)
	Root        string   // the root set for the site or detected, relative to the project ("." for itself)
	File        string   // the project file that sets Root ("" when it's set on this machine or detected)
	Set         bool     // Root is set for the site (on this machine or in File) rather than detected
	Suggestions []string // folders with an index.php, relative to the project
}

// Config is everything the web server needs to know.
type Config struct {
	StorageDir string
	LogFile    string
	HTTPPort   int
	HTTPSPort  int
	// Bind is the listen host. macOS only lets non-root processes bind ports below 1024
	// on the wildcard address, so it is empty there and non-local clients are rejected per request.
	Bind     string
	Sites    []Site
	Waiting  []Waiting
	Rootless []Rootless
	// Shares are sites served to tunnels, each on a loopback port of its own (see Share).
	Shares []Share
	// TLD limits the names HTTPS answers for; Issue makes a certificate for one of them.
	TLD   string
	Issue func(name string) (*tls.Certificate, error)
}

// hosts are the names one of a site's names (its domain or an alias) answers to: the name itself
// and up to config.MaxDepth levels of subdomains.
func hosts(name string) []string {
	var hosts []string
	prefix := ""
	for range config.MaxDepth + 1 {
		hosts = append(hosts, prefix+name)
		prefix += "*."
	}
	return hosts
}

// Caddyfile renders the configuration. It is exported for tests.
func Caddyfile(cfg Config) string {
	var b strings.Builder

	fmt.Fprintf(&b, `{
	admin off
	persist_config off
	auto_https disable_certs
	skip_install_trust
	http_port %d
	https_port %d
	storage file_system %s
	log {
		output file %s
		level WARN
	}
}

(flock_site) {
	@remote not client_ip 127.0.0.0/8 ::1
	abort @remote
	encode zstd gzip
}
`, cfg.HTTPPort, cfg.HTTPSPort, quote(cfg.StorageDir), quote(cfg.LogFile))

	// Every certificate comes from Flock (see certSource): the site's own file, or one issued for the name.
	const tlsBlock = "\ttls {\n\t\tget_certificate flock\n\t}\n"

	// Each of a site's names gets blocks of its own, for both schemes. Caddy tries the block with
	// the longest name first, so an alias under another site's domain (accounts.shop.test) wins
	// over that site's subdomains; one block per site would rank by the site's longest name instead.
	for _, s := range cfg.Sites {
		for _, name := range append([]string{s.Domain}, s.Aliases...) {
			hosts := hosts(name)

			if s.CertFile != "" {
				fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s%s}\n", addresses("https", hosts, cfg), tlsBlock, app(s))
				fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n\tredir https://{host}%s{uri} 308\n}\n",
					addresses("http", hosts, cfg), portSuffix(cfg.HTTPSPort, 443))
				continue
			}

			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s}\n", addresses("http", hosts, cfg), app(s))
			// Over HTTPS, a site that isn't secured names itself, so the page asks to secure it rather than link it.
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s\tvars flock_site %s\n%s}\n",
				addresses("https", hosts, cfg), tlsBlock, quote(siteName(s.Domain, cfg.TLD)), unlinked(cfg))
		}
	}

	// A site waiting for its PHP names itself, the version, the state and the project file that pins it.
	for _, w := range cfg.Waiting {
		vars := fmt.Sprintf("\tvars {\n\t\tflock_php_site %s\n\t\tflock_php %s\n\t\tflock_php_state %s\n\t\tflock_php_file %s\n\t}\n",
			quote(w.Name), quote(w.PHP), quote(w.State), quote(w.File))
		for _, name := range append([]string{w.Domain}, w.Aliases...) {
			hosts := hosts(name)
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s%s}\n", addresses("http", hosts, cfg), vars, unlinked(cfg))
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s%s%s}\n", addresses("https", hosts, cfg), tlsBlock, vars, unlinked(cfg))
		}
	}

	// A site without a usable document root says why, with the root, the file that sets it and the
	// folders that could be the root.
	for _, r := range cfg.Rootless {
		vars := rootlessVars(r)
		for _, name := range append([]string{r.Domain}, r.Aliases...) {
			hosts := hosts(name)
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s%s}\n", addresses("http", hosts, cfg), vars, unlinked(cfg))
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s%s%s}\n", addresses("https", hosts, cfg), tlsBlock, vars, unlinked(cfg))
		}
	}

	// Any other name gets the "not linked" page, its host and scheme filled in by templates.
	fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s}\n", addresses("http", []string{""}, cfg), unlinked(cfg))
	// Without this, https:// to an unknown name would fail the handshake (or hang).
	fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s%s}\n", addresses("https", []string{""}, cfg), tlsBlock, unlinked(cfg))

	for _, s := range cfg.Shares {
		b.WriteString(shareBlock(s))
	}

	return b.String()
}

// rootlessVars sets {http.vars.flock_root_*} for a site without a usable document root: the site's
// name, why, the root and whether it's set for the site, the file that sets it, and the suggested
// roots (separated by |).
func rootlessVars(r Rootless) string {
	set := ""
	if r.Set {
		set = "1"
	}
	return fmt.Sprintf("\tvars {\n\t\tflock_root_site %s\n\t\tflock_root_state %s\n\t\tflock_root %s\n\t\tflock_root_set %s\n\t\tflock_root_file %s\n\t\tflock_root_suggestions %s\n\t}\n",
		quote(r.Name), quote(r.State), quote(r.Root), quote(set), quote(r.File), quote(strings.Join(r.Suggestions, "|")))
}

// siteName is the name the CLI takes for a domain: watchtower for watchtower.test.
func siteName(domain, tld string) string {
	if tld != "" {
		return strings.TrimSuffix(domain, "."+tld)
	}
	if i := strings.LastIndex(domain, "."); i > 0 {
		return domain[:i]
	}
	return domain
}

//go:embed pages/unlinked.html
var unlinkedPage []byte

// pagesDir holds the pages Flock serves itself; Apply writes them.
func pagesDir(cfg Config) string { return filepath.Join(cfg.StorageDir, "pages") }

// unlinked serves the catch-all page: a 404 that is never cached, so the site shows once it's linked.
// X-Flock-Page tells the page's script it still gets this page, not the site (a site can 404 too).
func unlinked(cfg Config) string {
	return fmt.Sprintf("\troot * %s\n\trewrite * /unlinked.html\n\theader Cache-Control no-store\n\theader X-Flock-Page unlinked\n\ttemplates\n\tfile_server {\n\t\tstatus 404\n\t}\n",
		quote(pagesDir(cfg)))
}

// xdebugRequest matches requests that ask for Xdebug: the cookie the Xdebug browser extensions
// set (and Xdebug itself, after ?XDEBUG_SESSION_START), or a trigger in the query string.
const xdebugRequest = `{cookie.XDEBUG_SESSION} != "" || {cookie.XDEBUG_TRIGGER} != "" || ` +
	`{query.XDEBUG_SESSION} != "" || {query.XDEBUG_SESSION_START} != "" || {query.XDEBUG_TRIGGER} != ""`

func app(s Site) string {
	root := fmt.Sprintf("\troot * %s\n", quote(s.Root))
	serve := func(indent string) string { return php(s.FPM, indent) }
	if s.Proxy != "" {
		serve = func(indent string) string { return proxy(s.Proxy, indent) }
	}
	switch {
	case len(s.DebugFPM) == 0:
		return root + serve("\t")
	case s.Debugging:
		return root + php(s.DebugFPM, "\t")
	}
	return root + fmt.Sprintf("\t@xdebug expression `%s`\n\thandle @xdebug {\n%s\t}\n\thandle {\n%s\t}\n",
		xdebugRequest, php(s.DebugFPM, "\t\t"), serve("\t\t"))
}

// proxy sends every request to a FrankenPHP process, which serves the static files too. The Host
// header passes unchanged, so the app sees its own domain.
func proxy(upstream, indent string) string {
	return fmt.Sprintf("%sreverse_proxy %s\n", indent, quote(upstream))
}

// php sends PHP requests to upstreams and serves everything else as static files.
func php(upstreams []string, indent string) string {
	quoted := make([]string, len(upstreams))
	for i, u := range upstreams {
		quoted[i] = quote(u)
	}
	// php-cgi workers (Windows) serve one request at a time: send each request to the least busy
	// one and wait briefly for a free worker instead of failing.
	balance := ""
	if len(upstreams) > 1 {
		balance = fmt.Sprintf(" {\n%[1]s\tlb_policy least_conn\n%[1]s\tlb_try_duration 30s\n%[1]s}", indent)
	}
	return fmt.Sprintf("%[1]sphp_fastcgi %[2]s%[3]s\n%[1]sfile_server\n", indent, strings.Join(quoted, " "), balance)
}

func addresses(scheme string, hosts []string, cfg Config) string {
	port := cfg.HTTPPort
	if scheme == "https" {
		port = cfg.HTTPSPort
	}

	parts := make([]string, len(hosts))
	for i, h := range hosts {
		parts[i] = fmt.Sprintf("%s://%s:%d", scheme, h, port)
	}
	return strings.Join(parts, ", ")
}

func portSuffix(port, standard int) string {
	if port == standard {
		return ""
	}
	return ":" + strconv.Itoa(port)
}

// quote makes a Caddyfile token. Windows paths get forward slashes, which Caddy and Windows
// both accept: strconv.Quote would double every backslash, and the Caddyfile lexer keeps them.
func quote(s string) string {
	if isWindowsPath(s) {
		s = strings.ReplaceAll(s, `\`, "/")
	}
	return strconv.Quote(s)
}

func isWindowsPath(s string) bool {
	return strings.HasPrefix(s, `\\`) || (len(s) > 2 && s[1] == ':' && (s[2] == '\\' || s[2] == '/'))
}

// Apply (re)loads Caddy with cfg. Safe to call repeatedly; Caddy swaps configs gracefully.
func Apply(cfg Config) error {
	if err := os.MkdirAll(cfg.StorageDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(pagesDir(cfg), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(pagesDir(cfg), "unlinked.html"), unlinkedPage, 0o600); err != nil {
		return err
	}

	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("caddyfile adapter not registered")
	}

	json, warnings, err := adapter.Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		return fmt.Errorf("adapt web config: %w", err)
	}
	for _, w := range warnings {
		if w.Message != "" && !strings.Contains(w.Message, "formatted") {
			fmt.Fprintf(os.Stderr, "web config warning: %s\n", w.String())
		}
	}

	json = withBind(json, cfg.Bind)

	source, err := newCertSource(cfg)
	if err != nil {
		return err
	}
	previous := currentCerts.Swap(source)
	if err := caddy.Load(json, true); err != nil {
		currentCerts.Store(previous)
		return err
	}
	return nil
}

// Stop shuts the web server down.
func Stop() error {
	return caddy.Stop()
}
