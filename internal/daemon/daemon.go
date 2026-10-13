// Package daemon runs Flock's long-lived services — DNS, web server, PHP-FPM pools,
// mail catcher, databases — and exposes them through the local API.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"flock/internal/api"
	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/dns"
	"flock/internal/dumps"
	"flock/internal/mail"
	"flock/internal/paths"
	"flock/internal/php"
	"flock/internal/phpbuild"
	"flock/internal/ports"
	"flock/internal/postgres"
	"flock/internal/supervisor"
	"flock/internal/valkey"
	"flock/internal/web"
)

// Config controls a daemon instance. Everything else comes from <Root>/config.json.
type Config struct {
	Root    string
	Version string
}

// retryInterval is how often a degraded daemon tries to start what failed.
var retryInterval = 15 * time.Second

// ErrAlreadyRunning is returned when another daemon owns the socket.
var ErrAlreadyRunning = errors.New("flock daemon is already running")

type daemon struct {
	root      string
	version   string
	startedAt time.Time
	procs     *supervisor.Supervisor

	// reloadMu serialises reloads (API requests and the retry loop); mutateMu serialises config edits.
	reloadMu sync.Mutex
	mutateMu sync.Mutex
	dns      *dns.Server
	mail     *mail.Server
	webUp    bool
	builds   *phpbuild.Manager

	mu       sync.Mutex
	sites    []api.SiteStatus
	warnings []string
	cfg      *config.Config
}

func (d *daemon) shutdown() {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	if d.webUp {
		_ = web.Stop()
	}
	if d.dns != nil {
		_ = d.dns.Shutdown()
	}
	if d.mail != nil {
		_ = d.mail.Shutdown()
	}
	d.procs.StopAll()
}

type tcpAPI struct {
	ln    net.Listener
	token string
}

func (d *daemon) listenTCP() (*tcpAPI, error) {
	d.mu.Lock()
	port := 0
	if d.cfg != nil {
		port = d.cfg.Ports.API
	}
	d.mu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("%s", busy(port, err))
	}

	token, err := api.WriteInfo(d.root, "http://"+ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, err
	}
	return &tcpAPI{ln: ln, token: token}, nil
}

func (d *daemon) currentWarnings() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.warnings...)
}

func (d *daemon) setWarnings(w []string) {
	d.mu.Lock()
	d.warnings = w
	d.mu.Unlock()
}

func (d *daemon) degraded() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.warnings) > 0
}

// ensureDNS (re)starts the DNS server when its address or TLD changed. Windows uses the hosts file instead.
func (d *daemon) ensureDNS(cfg *config.Config) error {
	if runtime.GOOS == "windows" {
		return nil
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Ports.DNS)
	if d.dns != nil && d.dns.Addr() == addr && d.dns.TLD() == cfg.TLD {
		return nil
	}
	if d.dns != nil {
		_ = d.dns.Shutdown()
		d.dns = nil
	}

	srv := dns.New(addr, cfg.TLD)
	if err := srv.Start(); err != nil {
		return fmt.Errorf("dns: %s", busy(cfg.Ports.DNS, err))
	}
	d.dns = srv
	return nil
}

// ensureMail (re)starts the SMTP catcher when its port changed.
func (d *daemon) ensureMail(cfg *config.Config) error {
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Ports.SMTP)
	if d.mail != nil && d.mail.Addr() == addr {
		return nil
	}
	if d.mail != nil {
		_ = d.mail.Shutdown()
		d.mail = nil
	}

	srv := mail.New(addr, filepath.Join(d.root, "mail"))
	if err := srv.Start(); err != nil {
		return fmt.Errorf("mail: %s", busy(cfg.Ports.SMTP, err))
	}
	d.mail = srv
	return nil
}

// busy turns "address already in use" into a message naming the program that holds the port.
func busy(port int, err error) string {
	if strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "permission denied") {
		return ports.Describe(port)
	}
	return err.Error()
}

// Run starts all services and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := os.MkdirAll(cfg.Root, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", cfg.Root, err)
	}

	socket := paths.Socket(cfg.Root)
	if err := claimSocket(socket); err != nil {
		return err
	}

	d := &daemon{root: cfg.Root, version: cfg.Version, startedAt: time.Now(), procs: supervisor.New()}
	defer d.shutdown()

	if runtime.GOOS == "darwin" {
		d.builds = phpbuild.NewManager(cfg.Root, phpbuild.DefaultSources(), phpbuild.Hooks{
			Installed: d.installedPHP,
			Install:   d.installPHP,
			AutoBuild: func() bool {
				c, err := config.Load(d.root)
				return err == nil && c.PHPAutoBuild
			},
		})
		go d.builds.Run(ctx)
	}

	// A broken config or a busy port must not kill the daemon: launchd would restart it in a loop
	// and `flock status` could not explain what is wrong. Problems become warnings instead.
	if err := d.reload(); err != nil {
		d.setWarnings([]string{err.Error()})
	}

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	defer os.Remove(socket)

	apiServer := &http.Server{
		Handler:           api.NewHandler(d, ""),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- apiServer.Serve(ln) }()

	// The native apps can't speak HTTP over a unix socket with URLSession, so they get a
	// loopback TCP listener guarded by a random token published in api.json (mode 0600).
	if tcp, err := d.listenTCP(); err != nil {
		d.setWarnings(append(d.currentWarnings(), "api: "+err.Error()))
	} else {
		tcpServer := &http.Server{Handler: api.NewHandler(d, tcp.token), ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = tcpServer.Serve(tcp.ln) }()
		defer tcpServer.Close()
		defer os.Remove(api.InfoFile(d.root))
	}

	// Retry while degraded, so e.g. the web server comes up by itself once Lerd frees port 80.
	retry := time.NewTicker(retryInterval)
	defer retry.Stop()

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case err := <-serveErr:
			return fmt.Errorf("api server: %w", err)
		case <-retry.C:
			if d.degraded() {
				if err := d.reload(); err != nil {
					d.setWarnings([]string{err.Error()})
				}
			}
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return apiServer.Shutdown(shutdownCtx)
}

// Reload implements api.Backend.
func (d *daemon) Reload() error { return d.reload() }

// Root implements api.Backend.
func (d *daemon) Root() string { return d.root }

// Mutate implements api.Backend: it edits config.json and applies it.
// Changes are serialised so two UI requests cannot overwrite each other's edits.
func (d *daemon) Mutate(change func(*config.Config) error) error {
	d.mutateMu.Lock()
	defer d.mutateMu.Unlock()

	cfg, err := config.Load(d.root)
	if err != nil {
		return err
	}
	if err := change(cfg); err != nil {
		return err
	}
	if err := cfg.Save(d.root); err != nil {
		return err
	}
	return d.reload()
}

// reload re-reads config.json and reconciles every service with it.
// It only returns an error when the config itself is unusable; a service that fails
// to start is reported as a warning and retried later.
func (d *daemon) reload() error {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	cfg, err := config.Load(d.root)
	if err != nil {
		return err
	}

	ca, err := certs.Ensure(filepath.Join(d.root, "certs"), cfg.TLD)
	if err != nil {
		return fmt.Errorf("certificate authority: %w", err)
	}
	if err := ca.WriteBundle(php.CABundle(d.root), "/etc/ssl/cert.pem"); err != nil {
		return err
	}

	var warnings []string
	for _, ensure := range []func(*config.Config) error{d.ensureDNS, d.ensureMail} {
		if err := ensure(cfg); err != nil {
			warnings = append(warnings, err.Error())
		}
	}

	var (
		statuses []api.SiteStatus
		webSites []web.Site
		keep     = map[string]bool{}
		pools    = map[string]bool{}
	)

	installed, _ := php.Installed(d.root)
	for _, version := range installed {
		if err := php.SetXdebug(d.root, version, cfg.Xdebug); err != nil {
			warnings = append(warnings, "xdebug: "+err.Error())
		}
	}

	for _, site := range cfg.AllSites() {
		domain := cfg.Domain(site)
		version := cfg.PHPFor(site)
		status := api.SiteStatus{Name: site.Name, Domain: domain, Path: site.Path, PHP: version, Node: cfg.NodeFor(site), Secured: site.Secured, Parked: site.Parked}

		if !contains(installed, version) {
			status.Problem = fmt.Sprintf("PHP %s is not installed", version)
			statuses = append(statuses, status)
			continue
		}

		if !pools[version] {
			var extraEnv []string
			if cfg.Dumps {
				extraEnv = dumps.Env(cfg.Ports.Dumps)
			}
			spec, err := php.FPMSpec(d.root, version, extraEnv...)
			if err != nil {
				return err
			}
			if d.procs.Ensure(spec) {
				// Don't route requests to a pool that isn't listening yet (Caddy would answer 502).
				if !waitForSocket(php.Socket(d.root, version), 5*time.Second) {
					warnings = append(warnings, fmt.Sprintf("php %s: FPM did not start — see `flock logs php%s-fpm`", version, version))
				}
			}
			keep[spec.Name] = true
			pools[version] = true
		}

		ws := web.Site{Domain: domain, Root: web.DocumentRoot(site.Path), FPM: "unix/" + php.Socket(d.root, version)}
		scheme := "http"
		if site.Secured {
			if ws.CertFile, ws.KeyFile, err = ca.SiteCert(domain); err != nil {
				return fmt.Errorf("certificate for %s: %w", domain, err)
			}
			scheme = "https"
		}
		status.URL = scheme + "://" + domain + portSuffix(scheme, cfg)

		webSites = append(webSites, ws)
		statuses = append(statuses, status)
	}

	if v := cfg.Services.Postgres; v != "" && cfg.Services.Running("postgres") {
		spec, err := d.postgresSpec(cfg, v)
		switch {
		case err != nil:
			warnings = append(warnings, "postgres: "+err.Error())
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", cfg.Ports.Postgres):
			// Starting would only crash-loop on "could not bind".
			warnings = append(warnings, "postgres: "+ports.Describe(cfg.Ports.Postgres))
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
		}
	}

	if cfg.Dumps && len(installed) > 0 {
		spec, err := dumps.Spec(d.root, php.Binary(d.root, installed[0]), cfg.Ports.Dumps)
		switch {
		case err != nil:
			warnings = append(warnings, "dumps: "+err.Error())
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", cfg.Ports.Dumps):
			warnings = append(warnings, "dumps: "+ports.Describe(cfg.Ports.Dumps))
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
		}
	}

	if v := cfg.Services.Redis; v != "" && cfg.Services.Running("redis") {
		spec, err := valkey.Spec(d.root, v, cfg.Ports.Redis)
		switch {
		case err != nil:
			warnings = append(warnings, "redis: "+err.Error())
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", cfg.Ports.Redis):
			warnings = append(warnings, "redis: "+ports.Describe(cfg.Ports.Redis))
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
		}
	}

	d.procs.Retain(keep)

	bind := "127.0.0.1"
	if runtime.GOOS == "darwin" && (cfg.Ports.HTTP < 1024 || cfg.Ports.HTTPS < 1024) {
		bind = ""
	}

	if err := d.applyWeb(cfg, bind, webSites); err != nil {
		warnings = append(warnings, "web: "+err.Error())
	}

	d.mu.Lock()
	d.cfg, d.sites, d.warnings = cfg, statuses, warnings
	d.mu.Unlock()

	return nil
}

// applyWeb (re)configures Caddy. Before the first successful start it checks the ports itself,
// because Caddy's own error does not say who holds them.
func (d *daemon) applyWeb(cfg *config.Config, bind string, sites []web.Site) error {
	owned := map[int]bool{}
	if d.webUp && d.cfg != nil {
		owned[d.cfg.Ports.HTTP], owned[d.cfg.Ports.HTTPS] = true, true
	}
	for _, port := range []int{cfg.Ports.HTTP, cfg.Ports.HTTPS} {
		if !owned[port] && !ports.Free(bind, port) {
			return errors.New(ports.Describe(port))
		}
	}

	err := web.Apply(web.Config{
		StorageDir: filepath.Join(d.root, "caddy"),
		LogFile:    filepath.Join(d.root, "log", "web.log"),
		HTTPPort:   cfg.Ports.HTTP,
		HTTPSPort:  cfg.Ports.HTTPS,
		Bind:       bind,
		Sites:      sites,
	})
	if err != nil {
		return err
	}
	d.webUp = true
	return nil
}

func (d *daemon) procUp(name string) bool {
	for _, p := range d.procs.Status() {
		if p.Name == name {
			return p.Up
		}
	}
	return false
}

func (d *daemon) postgresSpec(cfg *config.Config, version string) (supervisor.Spec, error) {
	inst, err := postgres.NewInstaller(d.root)
	if err != nil {
		return supervisor.Spec{}, err
	}
	if !inst.Installed(version) {
		return supervisor.Spec{}, fmt.Errorf("version %s is not installed", version)
	}

	data := PostgresDataDir(d.root)
	if err := postgres.InitDB(inst.BinDir(version), data); err != nil {
		return supervisor.Spec{}, err
	}

	return postgres.Spec(inst.BinDir(version), data, filepath.Join(d.root, "run"),
		filepath.Join(d.root, "log", "postgres.log"), cfg.Ports.Postgres), nil
}

// PostgresDataDir is where the database cluster lives (shared across minor upgrades).
func PostgresDataDir(root string) string {
	return filepath.Join(root, "services", "postgres", "data")
}

// Status implements api.Backend.
func (d *daemon) Status() api.Status {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := api.Status{
		Version:   d.version,
		PID:       os.Getpid(),
		StartedAt: d.startedAt,
		Sites:     d.sites,
		Processes: d.procs.Status(),
		Warnings:  d.warnings,
	}
	if d.mail != nil {
		st.Mail = d.mail.Addr()
	}
	if messages, err := mail.List(filepath.Join(d.root, "mail")); err == nil {
		st.MailCount = len(messages)
	}
	st.DumpCount = dumps.Count(d.root)
	if d.dns != nil {
		st.DNS = api.DNSStatus{TLD: d.dns.TLD(), Addr: d.dns.Addr()}
	}
	if d.cfg != nil {
		st.HTTPPort, st.HTTPSPort = d.cfg.Ports.HTTP, d.cfg.Ports.HTTPS
		if d.cfg.Services.Postgres != "" {
			st.Postgres = fmt.Sprintf("%s on 127.0.0.1:%d", d.cfg.Services.Postgres, d.cfg.Ports.Postgres)
		}
		if d.cfg.Services.Redis != "" {
			st.Redis = fmt.Sprintf("Valkey %s on 127.0.0.1:%d", d.cfg.Services.Redis, d.cfg.Ports.Redis)
		}
		st.Xdebug = d.cfg.Xdebug
		st.Dumps = d.cfg.Dumps
	}

	return st
}

// PHPBuilds implements api.Backend.
func (d *daemon) PHPBuilds() *phpbuild.Manager { return d.builds }

func (d *daemon) installedPHP() map[string]string {
	installed, _ := php.Installed(d.root)
	out := map[string]string{}
	for _, minor := range installed {
		out[minor] = php.FullVersion(d.root, minor)
	}
	return out
}

// installPHP installs a finished build and restarts that version's FPM pool.
func (d *daemon) installPHP(dir string) error {
	if _, err := php.InstallFrom(d.root, dir); err != nil {
		return err
	}
	return d.reload()
}

// Services implements api.Backend.
func (d *daemon) Services() []api.Service {
	d.mu.Lock()
	cfg := d.cfg
	dnsUp, mailUp, webUp := d.dns != nil, d.mail != nil, d.webUp
	d.mu.Unlock()
	if cfg == nil {
		return []api.Service{}
	}
	p := cfg.Ports

	services := []api.Service{
		{ID: "web", Name: "Web server (Caddy)", Category: "Core", Port: p.HTTPS, Installed: true, Enabled: true, Running: webUp, Log: "web"},
		{ID: "mail", Name: "Mail (SMTP)", Category: "Mail", Port: p.SMTP, Installed: true, Enabled: true, Running: mailUp,
			Env: fmt.Sprintf("MAIL_MAILER=smtp\nMAIL_HOST=127.0.0.1\nMAIL_PORT=%d\nMAIL_USERNAME=${APP_NAME}\nMAIL_PASSWORD=null\nMAIL_SCHEME=null", p.SMTP),
			URL: fmt.Sprintf("smtp://127.0.0.1:%d", p.SMTP)},
		{ID: "dumps", Name: "Dump server", Category: "Debugging", Port: p.Dumps, Installed: true, Enabled: cfg.Dumps, Running: d.procUp("dump-server"), Stoppable: true,
			Env: fmt.Sprintf("VAR_DUMPER_FORMAT=server\nVAR_DUMPER_SERVER=127.0.0.1:%d", p.Dumps), Log: "dump-server"},
	}
	if runtime.GOOS != "windows" {
		services = append(services, api.Service{ID: "dns", Name: "DNS (*." + cfg.TLD + ")", Category: "Core", Port: p.DNS, Installed: true, Enabled: true, Running: dnsUp})
	}

	services = append(services, api.Service{
		ID: "postgres", Name: "PostgreSQL", Category: "Database", Version: cfg.Services.Postgres, Port: p.Postgres,
		Installed: cfg.Services.Postgres != "", Enabled: cfg.Services.Running("postgres"), Running: d.procUp("postgres"), Stoppable: true,
		Env: fmt.Sprintf("DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=postgres\nDB_PASSWORD=", p.Postgres),
		Log: "postgres", URL: fmt.Sprintf("postgres://postgres@127.0.0.1:%d", p.Postgres),
	}, api.Service{
		ID: "redis", Name: "Redis (Valkey)", Category: "Cache & Queue", Version: cfg.Services.Redis, Port: p.Redis,
		Installed: cfg.Services.Redis != "", Enabled: cfg.Services.Running("redis"), Running: d.procUp("valkey"), Stoppable: true,
		Env: fmt.Sprintf("REDIS_CLIENT=phpredis\nREDIS_HOST=127.0.0.1\nREDIS_PASSWORD=null\nREDIS_PORT=%d", p.Redis),
		Log: "valkey", URL: fmt.Sprintf("redis://127.0.0.1:%d", p.Redis),
	})

	return services
}

// waitForSocket polls until something accepts connections on a unix socket.
func waitForSocket(path string, timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
		if conn, err := net.DialTimeout("unix", path, 100*time.Millisecond); err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

func portSuffix(scheme string, cfg *config.Config) string {
	if scheme == "https" && cfg.Ports.HTTPS != 443 {
		return fmt.Sprintf(":%d", cfg.Ports.HTTPS)
	}
	if scheme == "http" && cfg.Ports.HTTP != 80 {
		return fmt.Sprintf(":%d", cfg.Ports.HTTP)
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// claimSocket refuses to start when a live daemon answers on socket, and removes a stale one left by a crash.
func claimSocket(socket string) error {
	if _, err := os.Stat(socket); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if conn, err := net.DialTimeout("unix", socket, time.Second); err == nil {
		_ = conn.Close()
		return ErrAlreadyRunning
	}

	if err := os.Remove(socket); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", socket, err)
	}

	return nil
}
