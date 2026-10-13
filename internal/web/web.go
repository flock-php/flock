// Package web is Flock's HTTP(S) front: an embedded Caddy that routes each
// site's domain (and subdomains) to the site's PHP-FPM pool.
//
// Caddy runs in-process, so there is no separate web server binary to ship,
// and it works the same on macOS and Windows.
package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	_ "github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

// Site is one served application.
type Site struct {
	Domain   string // e.g. watchtower.test (also serves *.watchtower.test)
	Root     string // document root, e.g. <path>/public
	FPM      string // php-fpm address: "unix/<socket>" or "127.0.0.1:9085"
	CertFile string // set when the site is secured
	KeyFile  string
}

// Config is everything the web server needs to know.
type Config struct {
	StorageDir string
	LogFile    string
	HTTPPort   int
	HTTPSPort  int
	// Bind is the listen host. macOS only lets non-root processes bind ports below 1024
	// on the wildcard address, so it is empty there and non-local clients are rejected per request.
	Bind  string
	Sites []Site
}

// DocumentRoot picks <path>/public when it has an index.php (Laravel and most frameworks), else path.
func DocumentRoot(path string) string {
	public := filepath.Join(path, "public")
	if _, err := os.Stat(filepath.Join(public, "index.php")); err == nil {
		return public
	}
	return path
}

// Caddyfile renders the configuration. It is exported for tests and `flock debug`.
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

	for _, s := range cfg.Sites {
		hosts := []string{s.Domain, "*." + s.Domain}

		if s.CertFile != "" {
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n\ttls %s %s\n%s}\n",
				addresses("https", hosts, cfg), quote(s.CertFile), quote(s.KeyFile), app(s))
			fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n\tredir https://{host}%s{uri} 308\n}\n",
				addresses("http", hosts, cfg), portSuffix(cfg.HTTPSPort, 443))
			continue
		}

		fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n%s}\n", addresses("http", hosts, cfg), app(s))
	}

	fmt.Fprintf(&b, "\n%s {\n\timport flock_site\n\trespond \"No Flock site is linked for {host}. Run `flock link` in the project directory.\" 404\n}\n",
		addresses("http", []string{""}, cfg))

	return b.String()
}

func app(s Site) string {
	return fmt.Sprintf("\troot * %s\n\tphp_fastcgi %s\n\tfile_server\n", quote(s.Root), quote(s.FPM))
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

func quote(s string) string {
	return strconv.Quote(s)
}

// Apply (re)loads Caddy with cfg. Safe to call repeatedly; Caddy swaps configs gracefully.
func Apply(cfg Config) error {
	if err := os.MkdirAll(cfg.StorageDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
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

	return caddy.Load(json, true)
}

// Stop shuts the web server down.
func Stop() error {
	return caddy.Stop()
}
