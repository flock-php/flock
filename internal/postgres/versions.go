package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// Errors of Manager operations; the API maps them to HTTP status codes.
var (
	ErrInvalidMajor = errors.New("invalid PostgreSQL version")
	ErrNotInstalled = errors.New("not installed")
	ErrBusy         = errors.New("another PostgreSQL operation is in progress")
)

// Service is how the Manager changes and observes the PostgreSQL service: the daemon (through
// config.json and a reload) or, without a daemon, the CLI (config.json only).
type Service interface {
	// Active returns the configured server version ("" when none) and whether it should run.
	Active() (version string, enabled bool)
	// Apply makes version the configured server ("" for none), switches the service on when
	// enable is set, and applies the change (stopping the old process, starting the new one).
	Apply(version string, enable bool) error
	// Running reports whether Flock's postgres process is up.
	Running() bool
	// Port is the PostgreSQL port.
	Port() int
	// Live reports whether Apply starts processes (a daemon), so the Manager waits for them.
	Live() bool
}

// Versions is the state of every offered major (GET /v1/postgres/versions).
type Versions struct {
	Active     string         `json:"active"`
	Running    bool           `json:"running"`
	Enabled    bool           `json:"enabled"`
	Port       int            `json:"port"`
	CheckedAt  *time.Time     `json:"checked_at"`
	CheckError string         `json:"check_error"`
	Majors     []MajorVersion `json:"majors"`
}

// MajorVersion is one major line with what is installed of it.
type MajorVersion struct {
	Major     string `json:"major"`
	Installed string `json:"installed"`
	Latest    string `json:"latest"`
	Update    bool   `json:"update"`
	Active    bool   `json:"active"`
	HasData   bool   `json:"has_data"`
	DataSize  int64  `json:"data_size"`
	EOL       string `json:"eol"`
	Busy      string `json:"busy"` // "" | "installing" | "updating" | "switching" | "uninstalling"
}

// Find returns the state of major.
func (v Versions) Find(major string) (MajorVersion, bool) {
	for _, m := range v.Majors {
		if m.Major == major {
			return m, true
		}
	}
	return MajorVersion{}, false
}

// ReadyTimeout is how long an operation waits for a (re)started postgres to accept connections.
var ReadyTimeout = 45 * time.Second

// Manager installs, updates, switches and removes PostgreSQL majors. One operation runs at a time:
// they all touch the single running server.
type Manager struct {
	Installer *Installer
	Catalog   *Catalog
	Service   Service

	op    sync.Mutex // held for the duration of an operation
	mu    sync.Mutex
	busy  map[string]string
	sizes map[string]sizeEntry
}

type sizeEntry struct {
	size int64
	at   time.Time
}

// NewManager creates a manager for the PostgreSQL installs under root.
func NewManager(root string, service Service) (*Manager, error) {
	inst, err := NewInstaller(root)
	if err != nil {
		return nil, err
	}
	catalog, err := NewCatalog(root)
	if err != nil {
		return nil, err
	}
	return &Manager{Installer: inst, Catalog: catalog, Service: service}, nil
}

func (m *Manager) root() string { return m.Installer.Root }

// InstalledVersions lists the installed server versions ("18.6.0"), newest first.
func InstalledVersions(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "services", "postgres"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var versions []string
	for _, e := range entries {
		if MajorOf(e.Name()) == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "services", "postgres", e.Name(), "bin", exe("postgres"))); err == nil {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return Newer(versions[i], versions[j]) })
	return versions, nil
}

// installedByMajor maps each installed major to its version: the active one, else the newest.
func (m *Manager) installedByMajor(active string) map[string]string {
	versions, _ := InstalledVersions(m.root())
	out := map[string]string{}
	for _, v := range versions {
		major := MajorOf(v)
		if _, ok := out[major]; !ok {
			out[major] = v
		}
	}
	if active != "" && m.Installer.Installed(active) {
		out[MajorOf(active)] = active
	}
	return out
}

// Versions returns the state of every major. It never waits on the network: when the catalog is
// stale it is refreshed in the background.
func (m *Manager) Versions() Versions {
	if m.Catalog.Stale() {
		m.Catalog.CheckInBackground()
	}
	return m.versions()
}

func (m *Manager) versions() Versions {
	latest, checkedAt, checkErr := m.Catalog.Latest()
	active, enabled := m.Service.Active()
	installed := m.installedByMajor(active)

	out := Versions{
		Active:     MajorOf(active),
		Running:    m.Service.Running(),
		Enabled:    enabled,
		Port:       m.Service.Port(),
		CheckError: checkErr,
		Majors:     []MajorVersion{},
	}
	if active != "" && !m.Installer.Installed(active) {
		out.Active = ""
	}
	if !checkedAt.IsZero() {
		out.CheckedAt = &checkedAt
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, major := range Majors {
		dir := DataDir(m.root(), major)
		v := MajorVersion{
			Major:     major,
			Installed: installed[major],
			Latest:    latest[major],
			Active:    major == out.Active,
			HasData:   HasCluster(dir),
			EOL:       EndOfLife[major],
			Busy:      m.busy[major],
		}
		v.Update = v.Installed != "" && v.Latest != "" && Newer(v.Latest, v.Installed)
		if v.HasData {
			v.DataSize = m.dataSize(major, dir)
		}
		out.Majors = append(out.Majors, v)
	}
	return out
}

// dataSize caches directory sizes for a while: UIs poll, and a big cluster has many files.
// Must be called with m.mu held.
func (m *Manager) dataSize(major, dir string) int64 {
	if m.sizes == nil {
		m.sizes = map[string]sizeEntry{}
	}
	if e, ok := m.sizes[major]; ok && time.Since(e.at) < 30*time.Second {
		return e.size
	}
	size := DirSize(dir)
	m.sizes[major] = sizeEntry{size: size, at: time.Now()}
	return size
}

// begin claims the operation lock for major, or fails with ErrBusy.
func (m *Manager) begin(major, what string) (func(), error) {
	if !slices.Contains(Majors, major) {
		return nil, fmt.Errorf("%w %q (Flock offers %v)", ErrInvalidMajor, major, Majors)
	}
	if !m.op.TryLock() {
		return nil, ErrBusy
	}
	m.mu.Lock()
	if m.busy == nil {
		m.busy = map[string]string{}
	}
	m.busy[major] = what
	delete(m.sizes, major)
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.busy, major)
		delete(m.sizes, major)
		m.mu.Unlock()
		m.op.Unlock()
	}, nil
}

// latest returns the newest version of major, fetching the catalog when it doesn't know one.
func (m *Manager) latest(ctx context.Context, major string) (string, error) {
	latest, _, _ := m.Catalog.Latest()
	if v := latest[major]; v != "" {
		return v, nil
	}
	if err := m.Catalog.Check(ctx); err != nil {
		return "", fmt.Errorf("look up PostgreSQL versions: %w", err)
	}
	latest, _, _ = m.Catalog.Latest()
	if v := latest[major]; v != "" {
		return v, nil
	}
	return "", fmt.Errorf("there is no PostgreSQL %s build for this platform", major)
}

// Install installs version (the newest release of major when "") and, when no major is active
// yet, makes it the active one and starts it. Installing a major that is installed already
// updates it when version is newer, and does nothing otherwise.
func (m *Manager) Install(ctx context.Context, major, version string) error {
	if version != "" && MajorOf(version) != major {
		return fmt.Errorf("%w %q: expected a %s.x.y version", ErrInvalidMajor, version, major)
	}
	done, err := m.begin(major, "installing")
	if err != nil {
		return err
	}
	defer done()

	if version == "" {
		if version, err = m.latest(ctx, major); err != nil {
			return err
		}
	}

	active, _ := m.Service.Active()
	if current := m.installedByMajor(active)[major]; current != "" {
		if !Newer(version, current) {
			return nil
		}
		m.setBusy(major, "updating")
		return m.update(ctx, major, current, version)
	}

	if err := m.Installer.Install(ctx, version); err != nil {
		return err
	}
	if active == "" || !m.Installer.Installed(active) {
		return m.activate(ctx, version, "")
	}
	return nil
}

// Update installs the newest release of an installed major, restarts it when it is the active
// one, and then removes the older releases of that major. The data is kept.
func (m *Manager) Update(ctx context.Context, major string) error {
	done, err := m.begin(major, "updating")
	if err != nil {
		return err
	}
	defer done()

	active, _ := m.Service.Active()
	current := m.installedByMajor(active)[major]
	if current == "" {
		return fmt.Errorf("PostgreSQL %s is %w", major, ErrNotInstalled)
	}
	latest, err := m.latest(ctx, major)
	if err != nil {
		return err
	}
	if !Newer(latest, current) {
		return nil
	}
	return m.update(ctx, major, current, latest)
}

func (m *Manager) update(ctx context.Context, major, current, latest string) error {
	if err := m.Installer.Install(ctx, latest); err != nil {
		return err
	}

	active, enabled := m.Service.Active()
	if active == current {
		if err := m.prepare(latest); err != nil {
			return err
		}
		if err := m.Service.Apply(latest, false); err != nil {
			return err
		}
		if enabled {
			if err := m.waitReady(ctx); err != nil {
				// Go back to the release that worked; the new one is not kept.
				_ = m.Service.Apply(current, false)
				_ = os.RemoveAll(m.Installer.Dir(latest))
				return fmt.Errorf("PostgreSQL %s did not start, so Flock kept %s: %w", latest, current, err)
			}
		}
	}
	m.removeOthers(major, latest)
	return nil
}

func (m *Manager) setBusy(major, what string) {
	m.mu.Lock()
	m.busy[major] = what
	m.mu.Unlock()
}

// removeOthers deletes every installed release of major except keep.
func (m *Manager) removeOthers(major, keep string) {
	versions, _ := InstalledVersions(m.root())
	for _, v := range versions {
		if MajorOf(v) == major && v != keep {
			_ = os.RemoveAll(m.Installer.Dir(v))
		}
	}
}

// Activate makes an installed major the one that runs: it stops the current server, creates the
// major's cluster when it has none, starts it, and switches the service on.
func (m *Manager) Activate(ctx context.Context, major string) error {
	done, err := m.begin(major, "switching")
	if err != nil {
		return err
	}
	defer done()

	active, _ := m.Service.Active()
	version := m.installedByMajor(active)[major]
	if version == "" {
		return fmt.Errorf("PostgreSQL %s is %w", major, ErrNotInstalled)
	}
	previous := ""
	if active != version && m.Installer.Installed(active) {
		previous = active
	}
	return m.activate(ctx, version, previous)
}

// activate starts version; when it doesn't come up, previous (if any) is started again.
func (m *Manager) activate(ctx context.Context, version, previous string) error {
	if err := m.prepare(version); err != nil {
		return err
	}
	if err := m.Service.Apply(version, true); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		if previous != "" {
			_ = m.Service.Apply(previous, true)
			return fmt.Errorf("PostgreSQL %s did not start, so Flock switched back to %s: %w", version, MajorOf(previous), err)
		}
		return fmt.Errorf("PostgreSQL %s did not start: %w", version, err)
	}
	return nil
}

// Uninstall removes the binaries of major, and its cluster when deleteData is set. When it was the
// active major, the newest other installed major takes over and is started.
func (m *Manager) Uninstall(ctx context.Context, major string, deleteData bool) error {
	done, err := m.begin(major, "uninstalling")
	if err != nil {
		return err
	}
	defer done()

	active, _ := m.Service.Active()
	installed := m.installedByMajor(active)
	if installed[major] == "" && !(deleteData && HasCluster(DataDir(m.root(), major))) {
		return fmt.Errorf("PostgreSQL %s is %w", major, ErrNotInstalled)
	}

	if active != "" && MajorOf(active) == major {
		next := ""
		for _, other := range Majors {
			if other != major && installed[other] != "" {
				next = installed[other]
				break
			}
		}
		if next != "" {
			if err := m.prepare(next); err != nil {
				return err
			}
		}
		// The newest other major takes over and is started (switched on when it was off).
		if err := m.Service.Apply(next, next != ""); err != nil {
			return err
		}
		if next != "" {
			// The uninstall goes ahead either way; a server that doesn't start shows in the status.
			_ = m.waitReady(ctx)
		}
	}

	m.removeOthers(major, "")
	if deleteData {
		if err := os.RemoveAll(DataDir(m.root(), major)); err != nil {
			return fmt.Errorf("delete the PostgreSQL %s data: %w", major, err)
		}
	}
	return nil
}

// prepare makes sure version's major has a cluster (moving the legacy one into place first), so
// a failing initdb is reported to the caller instead of only as a daemon warning.
func (m *Manager) prepare(version string) error {
	if err := MigrateLegacyDataDir(m.root()); err != nil {
		return err
	}
	return InitDB(m.Installer.BinDir(version), DataDir(m.root(), MajorOf(version)))
}

// waitReady waits until the service's postgres runs and accepts connections.
func (m *Manager) waitReady(ctx context.Context) error {
	if !m.Service.Live() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, ReadyTimeout)
	defer cancel()
	var lastErr error = errors.New("not running")
	for {
		if m.Service.Running() {
			pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
			lastErr = Exec(pingCtx, m.Service.Port(), "postgres", "SELECT 1")
			pingCancel()
			if lastErr == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%v (see `flock logs postgres`)", lastErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
