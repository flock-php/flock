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
	"sync"
	"time"

	"flock/internal/api"
	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/dns"
	"flock/internal/mail"
	"flock/internal/paths"
	"flock/internal/php"
	"flock/internal/postgres"
	"flock/internal/supervisor"
	"flock/internal/web"
)

// Config controls a daemon instance. Everything else comes from <Root>/config.json.
type Config struct {
	Root    string
	Version string
}

// ErrAlreadyRunning is returned when another daemon owns the socket.
var ErrAlreadyRunning = errors.New("flock daemon is already running")

type daemon struct {
	root      string
	version   string
	startedAt time.Time
	procs     *supervisor.Supervisor
	dns       *dns.Server
	mail      *mail.Server

	mu       sync.Mutex
	sites    []api.SiteStatus
	warnings []string
	cfg      *config.Config
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

	settings, err := config.Load(cfg.Root)
	if err != nil {
		return err
	}

	d := &daemon{root: cfg.Root, version: cfg.Version, startedAt: time.Now(), procs: supervisor.New()}
	defer d.procs.StopAll()

	// Windows resolves .test through the hosts file, so it does not need the DNS server.
	if runtime.GOOS != "windows" {
		d.dns = dns.New(fmt.Sprintf("127.0.0.1:%d", settings.Ports.DNS), settings.TLD)
		if err := d.dns.Start(); err != nil {
			return err
		}
		defer d.dns.Shutdown()
	}

	d.mail = mail.New(fmt.Sprintf("127.0.0.1:%d", settings.Ports.SMTP), filepath.Join(cfg.Root, "mail"))
	if err := d.mail.Start(); err != nil {
		return err
	}
	defer d.mail.Shutdown()

	if err := d.reload(); err != nil {
		return err
	}
	defer web.Stop()

	ln, err := net.Listen("unix", socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socket, err)
	}
	defer os.Remove(socket)

	apiServer := &http.Server{
		Handler:           api.NewHandler(d.status, d.reload),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- apiServer.Serve(ln) }()

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		return fmt.Errorf("api server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return apiServer.Shutdown(shutdownCtx)
}

// reload re-reads config.json and reconciles PHP pools, databases and the web server with it.
func (d *daemon) reload() error {
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

	var (
		warnings []string
		statuses []api.SiteStatus
		webSites []web.Site
		keep     = map[string]bool{}
		pools    = map[string]bool{}
	)

	installed, _ := php.Installed(d.root)
	for _, site := range cfg.AllSites() {
		domain := cfg.Domain(site)
		version := cfg.PHPFor(site)
		status := api.SiteStatus{Name: site.Name, Domain: domain, Path: site.Path, PHP: version, Secured: site.Secured, Parked: site.Parked}

		if !contains(installed, version) {
			status.Problem = fmt.Sprintf("PHP %s is not installed", version)
			statuses = append(statuses, status)
			continue
		}

		if !pools[version] {
			spec, err := php.FPMSpec(d.root, version)
			if err != nil {
				return err
			}
			d.procs.Ensure(spec)
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

	if v := cfg.Services.Postgres; v != "" {
		if spec, err := d.postgresSpec(cfg, v); err != nil {
			warnings = append(warnings, "postgres: "+err.Error())
		} else {
			d.procs.Ensure(spec)
			keep[spec.Name] = true
		}
	}

	d.procs.Retain(keep)

	bind := "127.0.0.1"
	if runtime.GOOS == "darwin" && (cfg.Ports.HTTP < 1024 || cfg.Ports.HTTPS < 1024) {
		bind = ""
	}

	err = web.Apply(web.Config{
		StorageDir: filepath.Join(d.root, "caddy"),
		LogFile:    filepath.Join(d.root, "log", "web.log"),
		HTTPPort:   cfg.Ports.HTTP,
		HTTPSPort:  cfg.Ports.HTTPS,
		Bind:       bind,
		Sites:      webSites,
	})
	if err != nil {
		return fmt.Errorf("web server: %w", err)
	}

	d.mu.Lock()
	d.cfg, d.sites, d.warnings = cfg, statuses, warnings
	d.mu.Unlock()

	return nil
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

func (d *daemon) status() api.Status {
	d.mu.Lock()
	defer d.mu.Unlock()

	st := api.Status{
		Version:   d.version,
		PID:       os.Getpid(),
		StartedAt: d.startedAt,
		Sites:     d.sites,
		Processes: d.procs.Running(),
		Warnings:  d.warnings,
		Mail:      d.mail.Addr(),
	}
	if d.dns != nil {
		st.DNS = api.DNSStatus{TLD: d.dns.TLD(), Addr: d.dns.Addr()}
	}
	if d.cfg != nil {
		st.HTTPPort, st.HTTPSPort = d.cfg.Ports.HTTP, d.cfg.Ports.HTTPS
		if d.cfg.Services.Postgres != "" {
			st.Postgres = fmt.Sprintf("%s on 127.0.0.1:%d", d.cfg.Services.Postgres, d.cfg.Ports.Postgres)
		}
	}

	return st
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
