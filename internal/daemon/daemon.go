// Package daemon runs Flock's long-lived services — DNS, web server, PHP pools,
// mail catcher, databases — and exposes them through the local API.
package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/certs"
	"flock/internal/components"
	"flock/internal/config"
	"flock/internal/dns"
	"flock/internal/dumps"
	"flock/internal/hosts"
	"flock/internal/mail"
	"flock/internal/node"
	"flock/internal/paths"
	"flock/internal/php"
	"flock/internal/phpbuild"
	"flock/internal/ports"
	"flock/internal/postgres"
	"flock/internal/project"
	"flock/internal/supervisor"
	"flock/internal/valkey"
	"flock/internal/web"
	"flock/internal/xdebug"
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
	dns      *dns.Server // written under reloadMu and mu
	dnsErr   error       // why the DNS server isn't running (Windows, where it is optional)
	mail     *mail.Server
	webUp    bool
	builds   *phpbuild.Manager
	// pg manages the PostgreSQL versions, components Valkey and the PostgreSQL client tools,
	// catalog the other services (MySQL, Redis, Meilisearch, ...).
	pg         *postgres.Manager
	components *components.Manager
	catalog    *catalog.Manager
	// migrateGlobals moves global npm packages out of Node's version directories, once per run.
	migrateGlobals sync.Once

	// leaves issues certificates for deep subdomains, aliases and unknown names during handshakes.
	leaves *certs.Leaves
	// checking is set while a wildcard check runs (see checkWildcard).
	checking atomic.Bool

	mu       sync.Mutex
	sites    []api.SiteStatus
	warnings []string
	problems map[string]string // why an installed catalog service or Valkey isn't running, by id
	// breakpoints finds PhpStorm breakpoints; breakpointSites is what the last reload found, by site path.
	breakpoints     xdebug.Detector
	breakpointSites map[string]bool
	cfg      *config.Config
	wildcard wildcardState
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

// windowsDNSPort is where the DNS server listens on Windows: NRPT rules can only name port 53.
// Tests move it out of the way.
var windowsDNSPort = dns.Port

// ensureDNS (re)starts the DNS server when its address or TLD changed. On Windows it is optional:
// the hosts file still resolves every site's domains when port 53 is taken, so a failure there is
// reported through the wildcard check instead of as a warning.
func (d *daemon) ensureDNS(cfg *config.Config) error {
	port := cfg.Ports.DNS
	if runtime.GOOS == "windows" {
		port = windowsDNSPort
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if d.dns != nil && d.dns.Addr() == addr && d.dns.TLD() == cfg.TLD {
		return nil
	}
	if d.dns != nil {
		_ = d.dns.Shutdown()
		d.setDNS(nil, nil)
	}

	srv := dns.New(addr, cfg.TLD)
	if err := srv.Start(); err != nil {
		err = fmt.Errorf("dns: %s", busy(port, err))
		d.setDNS(nil, err)
		if runtime.GOOS == "windows" {
			return nil
		}
		return err
	}
	d.setDNS(srv, nil)
	return nil
}

func (d *daemon) setDNS(srv *dns.Server, err error) {
	d.mu.Lock()
	d.dns, d.dnsErr = srv, err
	d.mu.Unlock()
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
// The last two are Windows' wording of EADDRINUSE and EACCES.
func busy(port int, err error) string {
	msg := err.Error()
	if strings.Contains(msg, "address already in use") || strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "Only one usage of each socket address") || strings.Contains(msg, "forbidden by its access permissions") {
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

	d := &daemon{root: cfg.Root, version: cfg.Version, startedAt: time.Now(), procs: supervisor.New(), leaves: certs.NewLeaves()}
	defer d.shutdown()

	if workspace, err := phpbuild.DefaultWorkspace(); err == nil && runtime.GOOS != "linux" {
		d.builds = phpbuild.NewManager(cfg.Root, workspace, phpbuild.DefaultSources(), phpbuild.Hooks{
			Installed: d.installedPHP,
			Install:   d.installPHP,
			AutoBuild: func() bool {
				c, err := config.Load(d.root)
				return err == nil && c.PHPAutoBuild
			},
		})
		go d.builds.Run(ctx)
	}

	if pg, err := postgres.NewManager(cfg.Root, pgService{d}); err == nil {
		d.pg = pg
		go pg.Catalog.Run(ctx)
	}
	d.components = components.NewManager(cfg.Root, components.Drivers(cfg.Root, d.Mutate))
	go d.components.Run(ctx)
	if err := catalog.MigrateLegacy(cfg.Root); err != nil {
		fmt.Fprintf(os.Stderr, "moving Redis into the catalog: %v\n", err)
	}
	d.catalog = catalog.NewManager(cfg.Root, catalog.Kinds(runtime.GOOS, runtime.GOARCH), d.Mutate)
	go d.catalog.Run(ctx)

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
	// Wildcard DNS can start or stop working without Flock changing anything (a VPN connects,
	// a policy arrives, port 53 frees up), so it is checked again regularly.
	wildcard := time.NewTicker(wildcardInterval)
	defer wildcard.Stop()
	// Follow the projects' version files, so a `git pull` that pins another PHP takes effect.
	watch := time.NewTicker(projectWatchInterval)
	defer watch.Stop()
	files := d.projectFiles()
	// Follow PhpStorm breakpoints, so a site switches to Xdebug as soon as one is set.
	breakpoints := time.NewTicker(breakpointInterval)
	defer breakpoints.Stop()

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
		case <-breakpoints.C:
			if d.breakpointsChanged() {
				if err := d.reload(); err != nil {
					d.setWarnings([]string{err.Error()})
				}
			}
		case <-wildcard.C:
			go d.recheckWildcard()
		case <-watch.C:
			if current := d.projectFiles(); current != files {
				files = current
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

// projectWatchInterval is how often the sites' project files are checked for changes.
var projectWatchInterval = 3 * time.Second

// projectFiles fingerprints the sites' project files (.flockrc, .nvmrc, .node-version, also in
// parent directories) by size and modification time.
func (d *daemon) projectFiles() string {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return ""
	}
	var b strings.Builder
	seen := map[string]bool{}
	for _, site := range cfg.AllSites() {
		for _, file := range project.Files(site.Path) {
			if seen[file] {
				continue
			}
			seen[file] = true
			if info, err := os.Stat(file); err == nil {
				fmt.Fprintf(&b, "%s %d %d\n", file, info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	return b.String()
}

// breakpointInterval is how often the sites' PhpStorm workspaces are checked for breakpoints.
var breakpointInterval = time.Second

// breakpointsChanged reports whether a site gained or lost PhpStorm breakpoints since the last reload.
func (d *daemon) breakpointsChanged() bool {
	d.mu.Lock()
	cfg, sites, found := d.cfg, d.sites, d.breakpointSites
	d.mu.Unlock()
	if cfg == nil || !cfg.Xdebug || !cfg.XdebugBreakpoints {
		return false
	}
	for _, site := range sites {
		if d.breakpoints.Has(site.Path) != found[site.Path] {
			return true
		}
	}
	return false
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
	if err := ca.WriteBundle(php.CABundle(d.root), certs.SystemRoots()); err != nil {
		return err
	}

	var warnings []string
	for _, ensure := range []func(*config.Config) error{d.ensureDNS, d.ensureMail} {
		if err := ensure(cfg); err != nil {
			warnings = append(warnings, err.Error())
		}
	}

	// Global npm packages of the default Node.js version are on the user's PATH via this link.
	if err := node.LinkCurrent(d.root, cfg.DefaultNode); err != nil {
		warnings = append(warnings, "node: "+err.Error())
	}
	d.migrateGlobals.Do(func() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := node.MigrateGlobals(ctx, d.root); err != nil {
				fmt.Fprintf(os.Stderr, "moving global npm packages into node-global: %v\n", err)
			}
		}()
	})

	var (
		statuses    []api.SiteStatus
		webSites    []web.Site
		domains     []string
		keep        = map[string]bool{}
		pools       = map[string][]string{} // version → FastCGI upstreams
		debugPools  = map[string][]string{} // version → upstreams of the pool that loads Xdebug
		breakpoints = map[string]bool{}     // site path → PhpStorm breakpoints found
	)

	// ensurePool starts a pool and waits for it to listen: Caddy would answer 502 until it does.
	ensurePool := func(pool php.Pool, log string) {
		restarted := false
		for _, spec := range pool.Specs {
			restarted = d.procs.Ensure(spec) || restarted
			keep[spec.Name] = true
		}
		if restarted && !php.WaitReady(pool.Upstreams, 5*time.Second) {
			warnings = append(warnings, fmt.Sprintf("php: %s did not start — see `flock logs %s`", log, log))
		}
	}

	installed, _ := php.Installed(d.root)

	sites := cfg.AllSites()
	extras := cfg.DomainsBySite(sites)
	for _, site := range sites {
		domain := cfg.Domain(site)
		phpVersion, nodeVersion := project.PHPFor(cfg, &site, site.Path), project.NodeFor(cfg, &site, site.Path)
		version := phpVersion.Version
		status := api.SiteStatus{
			Name: site.Name, Domain: domain, Path: site.Path, Secured: site.Secured, Parked: site.Parked,
			PHP: version, PHPSource: phpVersion.Source, PHPPinned: phpVersion.Pinned, PHPFile: phpVersion.File,
			Node: nodeVersion.Version, NodeSource: nodeVersion.Source, NodePinned: nodeVersion.Pinned, NodeFile: nodeVersion.File,
			NodeShadowed: nodeVersion.Shadowed, Domains: []api.SiteDomain{},
		}
		scheme := "http"
		if site.Secured {
			scheme = "https"
		}
		var aliases, served []string
		for _, e := range extras[site.Name] {
			sd := api.SiteDomain{Entry: e.Entry, Host: e.Host, Alias: e.Alias, Problem: e.Problem}
			if e.Problem == "" {
				sd.URL = scheme + "://" + e.Host + portSuffix(scheme, cfg)
				served = append(served, e.Host)
				if e.Alias {
					aliases = append(aliases, e.Host)
				}
			}
			status.Domains = append(status.Domains, sd)
		}

		if !contains(installed, version) {
			status.Problem = fmt.Sprintf("PHP %s is not installed", version)
			if phpVersion.Source == project.Project {
				status.Problem = fmt.Sprintf("PHP %s (pinned in %s) is not installed", version, filepath.Base(phpVersion.File))
			}
			statuses = append(statuses, status)
			continue
		}

		if pools[version] == nil {
			var extraEnv []string
			if cfg.Dumps {
				extraEnv = dumps.Env(cfg.Ports.Dumps)
			}
			pool, err := php.PoolFor(d.root, version, extraEnv...)
			if err != nil {
				return err
			}
			ensurePool(pool, php.PoolLog(version))
			pools[version] = pool.Upstreams

			if cfg.Xdebug {
				if debug, err := php.DebugPoolFor(d.root, version, extraEnv...); err != nil {
					warnings = append(warnings, "xdebug: "+err.Error())
				} else {
					ensurePool(debug, php.DebugPoolLog(version))
					debugPools[version] = debug.Upstreams
				}
			}
		}

		ws := web.Site{Domain: domain, Aliases: aliases, Root: web.DocumentRoot(site.Path), FPM: pools[version], DebugFPM: debugPools[version]}
		if cfg.Xdebug && cfg.XdebugBreakpoints {
			breakpoints[site.Path] = d.breakpoints.Has(site.Path)
			ws.Debugging = breakpoints[site.Path] && len(ws.DebugFPM) > 0
			status.Debugging = ws.Debugging
		}
		if site.Secured {
			if ws.CertFile, ws.KeyFile, err = ca.SiteCert(domain); err != nil {
				return fmt.Errorf("certificate for %s: %w", domain, err)
			}
		}
		status.URL = scheme + "://" + domain + portSuffix(scheme, cfg)

		webSites = append(webSites, ws)
		domains = append(append(domains, domain), served...)
		statuses = append(statuses, status)
	}

	if runtime.GOOS == "windows" {
		warnings = append(warnings, d.syncHosts(cfg, domains)...)
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

	problems := map[string]string{}
	// start runs an optional service on port, unless another Flock service is configured for
	// that port or something else holds it (it would only crash-loop on "address in use").
	start := func(id string, port int, spec supervisor.Spec, err error) {
		problem := ""
		if other, taken := cfg.UsedPorts(id)[port]; taken && other != id && err == nil {
			err = fmt.Errorf("port %d is also configured for %s — change one of the ports", port, serviceName(other))
		}
		switch {
		case err != nil:
			problem = err.Error()
		case !d.procUp(spec.Name) && !ports.Free("127.0.0.1", port):
			problem = ports.Describe(port)
		default:
			d.procs.Ensure(spec)
			keep[spec.Name] = true
			return
		}
		problems[id] = problem
		warnings = append(warnings, id+": "+problem)
	}

	if v := cfg.Services.Valkey; v != "" && cfg.Services.Running(components.Valkey) {
		spec, err := valkey.Spec(d.root, v, cfg.Ports.Valkey)
		start(components.Valkey, cfg.Ports.Valkey, spec, err)
	}
	if d.catalog != nil {
		for _, k := range d.catalog.Kinds() {
			if x, ok := cfg.Services.Extra(k.ID); ok && cfg.Services.Running(k.ID) {
				spec, err := catalog.Spec(d.root, k, x)
				start(k.ID, x.Port, spec, err)
			}
		}
	}

	d.procs.Retain(keep)

	bind := "127.0.0.1"
	if runtime.GOOS == "darwin" && (cfg.Ports.HTTP < 1024 || cfg.Ports.HTTPS < 1024) {
		bind = ""
	}

	if err := d.applyWeb(cfg, bind, webSites, ca); err != nil {
		warnings = append(warnings, "web: "+err.Error())
	}

	d.mu.Lock()
	d.cfg, d.sites, d.warnings, d.problems, d.breakpointSites = cfg, statuses, warnings, problems, breakpoints
	d.mu.Unlock()

	go d.checkWildcard()
	return nil
}

// applyWeb (re)configures Caddy. Before the first successful start it checks the ports itself,
// because Caddy's own error does not say who holds them.
func (d *daemon) applyWeb(cfg *config.Config, bind string, sites []web.Site, ca *certs.CA) error {
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
		TLD:        cfg.TLD,
		Issue:      func(name string) (*tls.Certificate, error) { return d.leaves.Get(ca, name) },
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

	// Older versions kept one cluster for every version; each major has its own now.
	if err := postgres.MigrateLegacyDataDir(d.root); err != nil {
		return supervisor.Spec{}, err
	}
	data := postgres.DataDir(d.root, postgres.MajorOf(version))
	if err := postgres.InitDB(inst.BinDir(version), data); err != nil {
		return supervisor.Spec{}, err
	}

	return postgres.Spec(inst.BinDir(version), data, filepath.Join(d.root, "run"),
		filepath.Join(d.root, "log", "postgres.log"), cfg.Ports.Postgres), nil
}

// PostgresVersions implements api.Backend.
func (d *daemon) PostgresVersions() *postgres.Manager { return d.pg }

// Components implements api.Backend.
func (d *daemon) Components() *components.Manager { return d.components }

// Catalog implements api.Backend.
func (d *daemon) Catalog() *catalog.Manager { return d.catalog }

// serviceName names a service for messages: "MySQL" for "mysql".
func serviceName(id string) string {
	switch id {
	case "web":
		return "the web server"
	case "dns", "api":
		return "Flock's " + strings.ToUpper(id)
	case "mail":
		return "the mail catcher"
	case "dumps":
		return "the dump server"
	case "postgres":
		return "PostgreSQL"
	case components.Valkey:
		return "Valkey"
	}
	for _, k := range catalog.Kinds(runtime.GOOS, runtime.GOARCH) {
		if k.ID == id {
			return k.Name
		}
	}
	return id
}

// pgService lets the postgres.Manager change the service through config.json and reloads.
type pgService struct{ d *daemon }

func (s pgService) Active() (string, bool) {
	cfg, err := config.Load(s.d.root)
	if err != nil {
		return "", false
	}
	return cfg.Services.Postgres, cfg.Services.Running("postgres")
}

func (s pgService) Apply(version string, enable bool) error {
	return s.d.Mutate(func(cfg *config.Config) error {
		cfg.Services.Postgres = version
		if enable {
			cfg.Services.SetRunning("postgres", true)
		}
		return nil
	})
}

func (s pgService) Running() bool { return s.d.procUp("postgres") }

func (s pgService) Port() int {
	if cfg, err := config.Load(s.d.root); err == nil {
		return cfg.Ports.Postgres
	}
	return config.Default().Ports.Postgres
}

func (s pgService) Live() bool { return true }

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
	st.DNS = api.DNSStatus{Wildcard: d.wildcard.ok, WildcardProblem: d.wildcard.problem, MaxDepth: config.MaxDepth}
	if d.cfg != nil {
		st.DNS.TLD = d.cfg.TLD
	}
	if d.dns != nil {
		st.DNS.Addr = d.dns.Addr()
	}
	if d.cfg != nil {
		st.HTTPPort, st.HTTPSPort = d.cfg.Ports.HTTP, d.cfg.Ports.HTTPS
		if d.cfg.Services.Postgres != "" {
			st.Postgres = fmt.Sprintf("%s on 127.0.0.1:%d", d.cfg.Services.Postgres, d.cfg.Ports.Postgres)
		}
		if d.cfg.Services.Valkey != "" {
			st.Valkey = fmt.Sprintf("%s on 127.0.0.1:%d", d.cfg.Services.Valkey, d.cfg.Ports.Valkey)
		}
		if d.catalog != nil {
			for _, k := range d.catalog.Kinds() {
				if x, ok := d.cfg.Services.Extra(k.ID); ok {
					st.Extras = append(st.Extras, api.ExtraStatus{ID: k.ID, Name: k.Name, Version: x.Version, Port: x.Port,
						Enabled: d.cfg.Services.Running(k.ID), Up: d.procUp(k.ID)})
				}
			}
		}
		st.Xdebug, st.XdebugBreakpoints = d.cfg.Xdebug, d.cfg.XdebugBreakpoints
		st.Dumps = d.cfg.Dumps

		// Without the helper, sites resolve only through the DNS rule: say so when that fails too.
		if runtime.GOOS == "windows" && d.wildcard.problem != "" && hosts.Blocked(d.root) && !hosts.HelperStatus().Installed {
			st.Warnings = append(st.Warnings[:len(st.Warnings):len(st.Warnings)],
				fmt.Sprintf("*.%s domains don't resolve: %s, and %s", d.cfg.TLD, d.wildcard.problem, helperBlockedProblem))
		}
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

// installPHP installs a finished build and restarts that version's pool.
func (d *daemon) installPHP(dir string) error {
	if runtime.GOOS == "windows" {
		// Windows locks the files of running programs: stop every PHP process first.
		// The minor of the build isn't known yet; reload brings everything back right after.
		d.ReleasePHP("")
	}
	if _, err := php.InstallFrom(d.root, dir); err != nil {
		_ = d.reload()
		return err
	}
	return d.reload()
}

// ReleasePHP implements api.Backend: on Windows it stops the processes that run a PHP version
// ("" for all), so its files can be replaced or deleted. The next reload starts what is needed.
func (d *daemon) ReleasePHP(minor string) {
	if runtime.GOOS != "windows" {
		return
	}
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	keep := map[string]bool{}
	for _, name := range d.procs.Running() {
		usesPHP := name == "dump-server" || strings.HasPrefix(name, "php"+minor)
		keep[name] = !usesPHP
	}
	d.procs.Retain(keep)
}

// Services implements api.Backend.
func (d *daemon) Services() []api.Service {
	d.mu.Lock()
	cfg := d.cfg
	dnsUp, mailUp, webUp, wildcard, problems := d.dns != nil, d.mail != nil, d.webUp, d.wildcard, d.problems
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
		services = append(services, api.Service{ID: "dns", Name: "DNS (*." + cfg.TLD + ")", Category: "Core", Port: p.DNS, Installed: true, Enabled: true, Running: dnsUp,
			Problem: wildcard.problem})
	} else {
		// Wildcard DNS (any subdomain) needs the DNS server and Windows' DNS rule; the hosts file
		// resolves the sites' domains and extra domains either way.
		services = append(services, api.Service{ID: "dns", Name: "DNS (*." + cfg.TLD + ")", Category: "Core", Port: windowsDNSPort,
			Installed: true, Enabled: true, Running: dnsUp && wildcard.ok, Problem: wildcard.problem})
		helper := hosts.HelperStatus()
		hostsService := api.Service{ID: "hosts", Name: "Hosts file", Category: "Core", Installed: helper.Installed, Enabled: true, Running: helper.Running}
		if !helper.Installed && hosts.Blocked(d.root) {
			hostsService.Problem = helperBlockedProblem
		}
		services = append(services, hostsService)
	}

	services = append(services, api.Service{
		ID: "postgres", Name: "PostgreSQL", Category: "Database", Version: cfg.Services.Postgres, Port: p.Postgres, Available: true,
		Installed: cfg.Services.Postgres != "", Enabled: cfg.Services.Running("postgres"), Running: d.procUp("postgres"), Stoppable: true,
		Env: fmt.Sprintf("DB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=%d\nDB_DATABASE=laravel\nDB_USERNAME=postgres\nDB_PASSWORD=", p.Postgres),
		Log: "postgres", URL: fmt.Sprintf("postgres://postgres@127.0.0.1:%d", p.Postgres),
	})

	valkeyService := api.Service{
		ID: components.Valkey, Name: "Valkey", Category: "Cache & Queue", Version: cfg.Services.Valkey, Port: p.Valkey,
		Description: "The open-source Redis: an in-memory store for cache, queues and sessions.", Available: true,
		Installed: cfg.Services.Valkey != "", Enabled: cfg.Services.Running(components.Valkey), Running: d.procUp("valkey"), Stoppable: true,
		Env: catalog.RedisEnv(p.Valkey), Log: "valkey", URL: fmt.Sprintf("redis://127.0.0.1:%d", p.Valkey), Problem: problems[components.Valkey],
	}
	if runtime.GOOS == "windows" {
		valkeyService.Available, valkeyService.Unavailable = false, "Valkey has no Windows build — install Redis instead."
	}
	services = append(services, valkeyService)

	if d.catalog != nil {
		for _, k := range d.catalog.Kinds() {
			s := api.Service{ID: k.ID, Name: k.Name, Category: k.Category, Description: k.Description,
				Available: k.Method != "", Unavailable: k.Unavailable, Log: k.ID}
			if x, ok := cfg.Services.Extra(k.ID); ok {
				s.Version, s.Port, s.Installed, s.Stoppable = x.Version, x.Port, true, true
				s.Enabled, s.Running, s.Problem = cfg.Services.Running(k.ID), d.procUp(k.ID), problems[k.ID]
				s.Env = k.Env(x.Port)
				if k.URL != nil {
					s.URL = k.URL(x.Port)
				}
			} else {
				s.Port = k.DefaultPort
			}
			services = append(services, s)
		}
	}

	return services
}

// helperBlockedProblem explains a hosts helper that `flock install` couldn't run.
const helperBlockedProblem = "Windows blocked the Flock hosts helper (Smart App Control or an App Control policy only runs signed programs as services)"

// syncHosts hands the site domains to the hosts helper (Windows) and explains when it can't apply them.
func (d *daemon) syncHosts(cfg *config.Config, domains []string) []string {
	if err := hosts.WriteSource(d.root, domains); err != nil {
		return []string{"hosts: " + err.Error()}
	}
	helper := hosts.HelperStatus()
	switch {
	case !helper.Installed && hosts.Blocked(d.root):
		return nil // sites resolve through the DNS rule; Status reports it if that fails too
	case !helper.Installed:
		return []string{fmt.Sprintf("*.%s domains don't resolve: the Flock hosts helper is not installed — run `flock install`", cfg.TLD)}
	case !helper.Running:
		return []string{fmt.Sprintf("*.%s domains don't resolve: the Flock hosts helper service is stopped — run `flock install`", cfg.TLD)}
	case !helper.Configured(hosts.SourceFile(d.root), cfg.TLD):
		return []string{fmt.Sprintf("the Flock hosts helper is set up for another domain or user — run `flock install` to switch it to *.%s", cfg.TLD)}
	}
	return nil
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
