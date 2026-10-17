// Package api is the local HTTP API between the Flock daemon and its clients
// (the CLI now, the native macOS/Windows apps later). It is served over a unix socket.
package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"flock/internal/catalog"
	"flock/internal/components"
	"flock/internal/config"
	"flock/internal/frankenphp"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/supervisor"
)

// ErrDaemonNotRunning is returned when nothing is listening on the socket.
var ErrDaemonNotRunning = errors.New("flock daemon is not running (start it with `flock daemon`)")

// Status describes the running daemon.
type Status struct {
	Version           string                  `json:"version"`
	PID               int                     `json:"pid"`
	StartedAt         time.Time               `json:"started_at"`
	DNS               DNSStatus               `json:"dns"`
	HTTPPort          int                     `json:"http_port"`
	HTTPSPort         int                     `json:"https_port"`
	Mail              string                  `json:"mail"`
	Dumps             bool                    `json:"dumps"`
	MailCount         int                     `json:"mail_count"`
	DumpCount         int                     `json:"dump_count"`
	Postgres          string                  `json:"postgres,omitempty"`
	Valkey            string                  `json:"valkey,omitempty"`
	Extras            []ExtraStatus           `json:"extras,omitempty"` // installed catalog services
	Xdebug            bool                    `json:"xdebug"`           // Xdebug detection; see config.Config.Xdebug
	XdebugBreakpoints bool                    `json:"xdebug_breakpoints"`
	Sites             []SiteStatus            `json:"sites"`
	Processes         []supervisor.ProcStatus `json:"processes"`
	Warnings          []string                `json:"warnings,omitempty"`
}

// ExtraStatus is an installed catalog service (MySQL, Redis, ...).
type ExtraStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Port    int    `json:"port"`
	Enabled bool   `json:"enabled"`
	Up      bool   `json:"up"`
}

// SiteStatus is one served site as the daemon sees it.
type SiteStatus struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
	// PHP and Node are the versions the site uses. Each Source is "isolated" (set for the site in
	// Flock), "project" (pinned in the project) or "default". Pinned is what the project pins, in
	// File, also when an isolation wins over it. NodeShadowed is a .nvmrc / .node-version that
	// .flockrc's node= wins over, e.g. ".nvmrc: 20".
	PHP          string `json:"php"`
	PHPSource    string `json:"php_source"`
	PHPPinned    string `json:"php_pinned"`
	PHPFile      string `json:"php_file"`
	Node         string `json:"node"`
	NodeSource   string `json:"node_source"`
	NodePinned   string `json:"node_pinned"`
	NodeFile     string `json:"node_file"`
	NodeShadowed string `json:"node_shadowed"`
	Secured      bool   `json:"secured"`
	Parked       bool   `json:"parked"`
	URL          string `json:"url,omitempty"`
	Problem      string `json:"problem,omitempty"`
	// Debugging is set while PhpStorm breakpoints send every request of the site to Xdebug.
	Debugging bool `json:"debugging,omitempty"`
	// Domains are the site's extra domains (subdomains and aliases), in the order they were added.
	Domains []SiteDomain `json:"domains"`
	// Server is what runs the site's PHP: "fastcgi" (the version's PHP-FPM pool; php-cgi on
	// Windows) or "frankenphp". Worker is FrankenPHP's worker mode: "" (classic) or "octane".
	// OctaneAvailable is set when the project has Laravel Octane installed.
	Server          string `json:"server"`
	Worker          string `json:"worker"`
	OctaneAvailable bool   `json:"octane_available"`
	// FrankenPHP is the state of the site's FrankenPHP process, when Server is "frankenphp". Until
	// it runs, the site is served through FastCGI.
	FrankenPHP *SiteFrankenPHP `json:"frankenphp,omitempty"`
	// Boost is whether the project uses Laravel Boost and has Flock's guideline (see package boost):
	// "" (no Boost), "missing", "current", "outdated" or "custom".
	Boost string `json:"boost,omitempty"`
}

// FrankenPHP states of a site.
const (
	FrankenPHPRunning     = "running"     // serving the site
	FrankenPHPInstalling  = "installing"  // the build for the site's PHP version is downloading
	FrankenPHPFailed      = "failed"      // see Problem; the site is served through FastCGI
	FrankenPHPUnavailable = "unavailable" // no build for this platform or PHP version; see Problem
)

// SiteFrankenPHP is a site's FrankenPHP process.
type SiteFrankenPHP struct {
	State      string  `json:"state"`
	Version    string  `json:"version,omitempty"`     // FrankenPHP, e.g. "1.13.0"
	PHPVersion string  `json:"php_version,omitempty"` // the embedded PHP, e.g. "8.4.14"
	Port       int     `json:"port"`                  // loopback port the web server proxies to
	Progress   float64 `json:"progress"`              // while installing: 0..1, or -1 when unknown
	Problem    string  `json:"problem,omitempty"`
	Log        string  `json:"log"` // name for /v1/logs/services/{name}
}

// SiteDomain is one of a site's extra domains.
type SiteDomain struct {
	Entry   string `json:"entry"` // as stored and passed to the API: "tenant1" or "my-app-2.test"
	Host    string `json:"host"`  // the full name: "tenant1.app.test"
	Alias   bool   `json:"alias"` // a name of its own (with its subdomains) rather than a subdomain
	URL     string `json:"url,omitempty"`
	Problem string `json:"problem,omitempty"` // why it isn't served, e.g. another site uses it
}

// DNSStatus describes how site names resolve.
type DNSStatus struct {
	TLD  string `json:"tld"`
	Addr string `json:"addr"` // the embedded DNS server, when it runs
	// Wildcard reports whether every name under the TLD resolves (any subdomain, at any depth).
	// When it doesn't, WildcardProblem says why. On Windows (the DNS rule is missing or overridden
	// by a policy) the sites' domains and extra domains still resolve, through the hosts file; on
	// macOS (/etc/resolver/<tld> missing or overridden) nothing under the TLD does.
	Wildcard        bool   `json:"wildcard"`
	WildcardProblem string `json:"wildcard_problem,omitempty"`
	MaxDepth        int    `json:"max_depth"` // subdomain levels below a site that are served
}

// Settings are the global toggles a UI can change; nil fields stay as they are.
type Settings struct {
	Xdebug            *bool   `json:"xdebug"`
	XdebugBreakpoints *bool   `json:"xdebug_breakpoints"`
	Dumps             *bool   `json:"dumps"`
	DefaultPHP        *string `json:"default_php"`
	DefaultNode       *string `json:"default_node"`
	// Channel is "stable", "beta" or "experimental" (see config.Config.Channel).
	Channel *string `json:"channel"`
}

// Backend is what the API needs from the daemon.
type Backend interface {
	Status() Status
	Reload() error
	Root() string
	// Mutate loads config.json, applies change, saves it and reloads the daemon.
	Mutate(change func(*config.Config) error) error
	// Services describes every service with its live state.
	Services() []Service
	// PHP installs PHP versions from the binaries manifest.
	PHP() *php.Manager
	// ReleasePHP stops whatever runs a PHP version so its files can be removed (Windows locks them).
	ReleasePHP(minor string)
	// PostgresVersions manages the PostgreSQL majors (nil when unavailable).
	PostgresVersions() *postgres.Manager
	// Components manages Valkey and the PostgreSQL client tools (nil when unavailable).
	Components() *components.Manager
	// Catalog manages MySQL, Redis, Meilisearch and the other catalog services (nil when unavailable).
	Catalog() *catalog.Manager
	// FrankenPHP installs FrankenPHP builds (nil when unavailable).
	FrankenPHP() *frankenphp.Manager
	// RestartSite restarts a FrankenPHP site's process, e.g. so Octane's workers load changed code.
	RestartSite(name string) error
}

// NewHandler builds the API routes. A non-empty token requires `Authorization: Bearer <token>`
// (used for the TCP listener the native apps talk to; the CLI's unix socket is protected by file permissions).
func NewHandler(b Backend, token string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/reload", func(w http.ResponseWriter, _ *http.Request) {
		if err := b.Reload(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /v1/settings", func(w http.ResponseWriter, r *http.Request) {
		var settings Settings
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&settings); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid settings: %w", err))
			return
		}
		err := b.Mutate(func(cfg *config.Config) error { return applySettings(b.Root(), cfg, settings) })
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, b.Status())
	})

	registerConfig(mux, b)
	registerPHPVersions(mux, b)
	registerExtensions(mux, b)
	registerNodeVersions(mux, b)
	registerPostgresVersions(mux, b)
	registerComponents(mux, b)
	registerCatalog(mux, b)
	registerFrankenPHP(mux, b)
	registerMail(mux, b)
	registerDumps(mux, b)
	registerLogs(mux, b)
	registerMCP(mux, b)
	registerBoost(mux, b)

	if token == "" {
		return mux
	}
	return requireToken(token, mux)
}

func requireToken(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("missing or invalid token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// Client talks to the daemon over its socket.
type Client struct {
	http *http.Client
}

// NewClient creates a client for the daemon listening on socket.
func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}

	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second}}
}

// Status fetches the daemon status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.get(ctx, "/v1/status", &status)

	return status, err
}

// Reload asks the daemon to apply config.json and returns the resulting status.
func (c *Client) Reload(ctx context.Context) (Status, error) {
	var status Status
	err := c.do(ctx, http.MethodPost, "/v1/reload", &status)

	return status, err
}

// UpdateSettings changes the given global settings and returns the resulting status.
func (c *Client) UpdateSettings(ctx context.Context, s Settings) (Status, error) {
	var status Status
	err := c.send(ctx, http.MethodPost, "/v1/settings", s, &status)
	return status, err
}

// slow is the client for requests that wait on the network or copy files (checks, installs).
func (c *Client) slow() *Client {
	return c.timeout(2 * time.Minute)
}

// timeout is the client for requests that may take up to d (installs, switching servers).
func (c *Client) timeout(d time.Duration) *Client {
	return &Client{http: &http.Client{Transport: c.http.Transport, Timeout: d}}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, out)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	return c.send(ctx, method, path, nil, out)
}

// send makes a request with body (encoded as JSON unless nil) and decodes the answer into out.
func (c *Client) send(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://flockd"+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrDaemonNotRunning
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error != "" {
			return errors.New(body.Error)
		}
		return fmt.Errorf("%s %s: unexpected status %s", method, path, resp.Status)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
