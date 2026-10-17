package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"flock/internal/binaries"
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
	// Active returns the configured server (an install ID, "" when none) and whether it should run.
	Active() (id string, enabled bool)
	// Apply makes id the configured server ("" for none), switches the service on when enable is
	// set, and applies the change (stopping the old process, starting the new one).
	Apply(id string, enable bool) error
	// Running reports whether Flock's postgres process is up.
	Running() bool
	// Port is the PostgreSQL port.
	Port() int
	// Live reports whether Apply starts processes (a daemon), so the Manager waits for them.
	Live() bool
}

// Versions is the state of every offered or installed major (GET /v1/postgres/versions).
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
	Major      string `json:"major"`
	Installed  string `json:"installed"` // version, e.g. "18.6"
	Latest     string `json:"latest"`    // newest version in the channel
	Update     bool   `json:"update"`    // Latest (or a newer build of it) can be installed
	Size       int64  `json:"size"`      // download size of Latest
	Prerelease bool   `json:"prerelease"`
	Yanked     string `json:"yanked"` // why the installed build was withdrawn, "" when it wasn't
	Active     bool   `json:"active"`
	HasData    bool   `json:"has_data"`
	DataSize   int64  `json:"data_size"`
	EOL        string `json:"eol"`
	Busy       string `json:"busy"` // "" | "installing" | "updating" | "switching" | "uninstalling"
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

// Manager installs, updates, switches and removes PostgreSQL majors from the binaries manifest.
// One operation runs at a time: they all touch the single running server.
type Manager struct {
	Installer *Installer
	Catalog   *binaries.Catalog
	Service   Service
	platform  string

	op    sync.Mutex // held for the duration of an operation
	mu    sync.Mutex
	busy  map[string]string
	sizes map[string]sizeEntry
}

type sizeEntry struct {
	size int64
	at   time.Time
}

// NewManager creates a manager for the PostgreSQL installs under root. A withdrawn (yanked) build
// is replaced by the next good one as soon as the manifest has it.
func NewManager(root string, catalog *binaries.Catalog, service Service) *Manager {
	m := &Manager{Installer: NewInstaller(root), Catalog: catalog, Service: service, platform: binaries.Platform()}
	if catalog != nil {
		catalog.OnChange(m.replaceYanked)
	}
	return m
}

func (m *Manager) root() string { return m.Installer.Root }

// InstalledIDs lists the installed builds ("18.6-build1"), newest first.
func InstalledIDs(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "services", "postgres"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if _, _, ok := ParseID(e.Name()); ok && Installed(root, e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	sort.Slice(ids, func(i, j int) bool { return Newer(ids[i], ids[j]) })
	return ids, nil
}

// installedByMajor maps each installed major to its build: the active one, else the newest.
func (m *Manager) installedByMajor(active string) map[string]string {
	ids, _ := InstalledIDs(m.root())
	out := map[string]string{}
	for _, id := range ids {
		if major := MajorOf(id); out[major] == "" {
			out[major] = id
		}
	}
	if active != "" && m.Installer.Installed(active) {
		out[MajorOf(active)] = active
	}
	return out
}

func (m *Manager) manifest() *binaries.Manifest {
	if m.Catalog == nil {
		return nil
	}
	return m.Catalog.Manifest()
}

// Check fetches the manifest now.
func (m *Manager) Check(ctx context.Context) error {
	if m.Catalog == nil {
		return errors.New("PostgreSQL isn't published for this platform")
	}
	_, err := m.Catalog.Check(ctx)
	return err
}

// Versions returns the state of every major the channel offers or that is installed or has
// data. It never waits on the network.
func (m *Manager) Versions() Versions {
	manifest := m.manifest()
	available := offers(manifest, m.platform)
	active, enabled := m.Service.Active()
	installed := m.installedByMajor(active)

	out := Versions{
		Active:  MajorOf(active),
		Running: m.Service.Running(),
		Enabled: enabled,
		Port:    m.Service.Port(),
		Majors:  []MajorVersion{},
	}
	if active != "" && !m.Installer.Installed(active) {
		out.Active = ""
	}
	if m.Catalog != nil {
		cs := m.Catalog.Status()
		out.CheckedAt, out.CheckError = cs.CheckedAt, cs.CheckError
	}

	majors := map[string]bool{}
	for major := range available {
		majors[major] = true
	}
	for major := range installed {
		majors[major] = true
	}
	if entries, err := os.ReadDir(DataRoot(m.root())); err == nil {
		for _, e := range entries {
			if majorPattern.MatchString(e.Name()) && HasCluster(DataDir(m.root(), e.Name())) {
				majors[e.Name()] = true
			}
		}
	}
	list := make([]string, 0, len(majors))
	for major := range majors {
		list = append(list, major)
	}
	sortMajors(list)

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, major := range list {
		dir := DataDir(m.root(), major)
		v := MajorVersion{
			Major:     major,
			Installed: VersionOf(installed[major]),
			Active:    major == out.Active,
			HasData:   HasCluster(dir),
			EOL:       EndOfLife[major],
			Busy:      m.busy[major],
		}
		offer, ok := available[major]
		if ok {
			v.Latest, v.Size, v.Prerelease = offer.Version, offer.Size, offer.Prerelease
		}
		if id := installed[major]; id != "" {
			v.Yanked = m.yanked(manifest, id)
			v.Update = ok && offer.Installable() && Newer(ID(offer.Version, offer.Build), id)
		}
		if v.HasData {
			v.DataSize = m.dataSize(major, dir)
		}
		out.Majors = append(out.Majors, v)
	}
	return out
}

// yanked is why an installed build was withdrawn, or "".
func (m *Manager) yanked(manifest *binaries.Manifest, id string) string {
	version, build, _ := ParseID(id)
	for _, v := range manifest.Versions(Package, m.platform) {
		if v.Version == version {
			return v.YankReason(build)
		}
	}
	return ""
}

// replaceYanked updates every installed major whose build was withdrawn, when a good one exists.
func (m *Manager) replaceYanked() {
	for _, v := range m.Versions().Majors {
		if v.Yanked != "" && v.Update && v.Busy == "" {
			go func(major string) { _ = m.Update(context.Background(), major) }(v.Major)
		}
	}
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
	if !majorPattern.MatchString(major) {
		return nil, fmt.Errorf("%w %q: give a major version like %s", ErrInvalidMajor, major, DefaultMajor)
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

// latest returns the channel's newest build of major, fetching the manifest first.
func (m *Manager) latest(ctx context.Context, major string) (binaries.Version, error) {
	if m.Catalog == nil {
		return binaries.Version{}, errors.New("PostgreSQL isn't published for this platform")
	}
	manifest, err := m.Catalog.Check(ctx)
	if err != nil {
		return binaries.Version{}, fmt.Errorf("look up PostgreSQL versions: %w", err)
	}
	v, ok := offers(manifest, m.platform)[major]
	if !ok {
		return binaries.Version{}, fmt.Errorf("%w %s: no build of it is published for this platform", ErrInvalidMajor, major)
	}
	if !v.Installable() {
		return binaries.Version{}, fmt.Errorf("PostgreSQL %s build %d was withdrawn: %s", v.Version, v.Build, v.YankReason(v.Build))
	}
	return v, nil
}

// Install installs the newest build of major and, when no major is active yet, makes it the
// active one and starts it. Installing a major that is installed already updates it when the
// channel has a newer build, and does nothing otherwise.
func (m *Manager) Install(ctx context.Context, major string) error {
	done, err := m.begin(major, "installing")
	if err != nil {
		return err
	}
	defer done()

	latest, err := m.latest(ctx, major)
	if err != nil {
		return err
	}

	active, _ := m.Service.Active()
	if current := m.installedByMajor(active)[major]; current != "" {
		if !Newer(ID(latest.Version, latest.Build), current) {
			return nil
		}
		m.setBusy(major, "updating")
		return m.update(ctx, major, current, latest)
	}

	id, err := m.Installer.Install(ctx, latest)
	if err != nil {
		return err
	}
	if active == "" || !m.Installer.Installed(active) {
		return m.activate(ctx, id, "")
	}
	return nil
}

// Update installs the newest build of an installed major, restarts it when it is the active one,
// and then removes the older builds of that major. The data is kept.
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
	if !Newer(ID(latest.Version, latest.Build), current) {
		return nil
	}
	return m.update(ctx, major, current, latest)
}

func (m *Manager) update(ctx context.Context, major, current string, latest binaries.Version) error {
	id, err := m.Installer.Install(ctx, latest)
	if err != nil {
		return err
	}

	active, enabled := m.Service.Active()
	if active == current {
		if err := m.prepare(id); err != nil {
			return err
		}
		if err := m.Service.Apply(id, false); err != nil {
			return err
		}
		if enabled {
			if err := m.waitReady(ctx); err != nil {
				// Go back to the build that worked; the new one is not kept.
				_ = m.Service.Apply(current, false)
				_ = os.RemoveAll(m.Installer.Dir(id))
				return fmt.Errorf("PostgreSQL %s did not start, so Flock kept %s: %w", VersionOf(id), VersionOf(current), err)
			}
		}
	}
	m.removeOthers(major, id)
	return nil
}

func (m *Manager) setBusy(major, what string) {
	m.mu.Lock()
	m.busy[major] = what
	m.mu.Unlock()
}

// removeOthers deletes every installed build of major except keep.
func (m *Manager) removeOthers(major, keep string) {
	ids, _ := InstalledIDs(m.root())
	for _, id := range ids {
		if MajorOf(id) == major && id != keep {
			_ = os.RemoveAll(m.Installer.Dir(id))
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
	id := m.installedByMajor(active)[major]
	if id == "" {
		return fmt.Errorf("PostgreSQL %s is %w", major, ErrNotInstalled)
	}
	previous := ""
	if active != id && m.Installer.Installed(active) {
		previous = active
	}
	return m.activate(ctx, id, previous)
}

// activate starts id; when it doesn't come up, previous (if any) is started again.
func (m *Manager) activate(ctx context.Context, id, previous string) error {
	if err := m.prepare(id); err != nil {
		return err
	}
	if err := m.Service.Apply(id, true); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		if previous != "" {
			_ = m.Service.Apply(previous, true)
			return fmt.Errorf("PostgreSQL %s did not start, so Flock switched back to %s: %w", MajorOf(id), MajorOf(previous), err)
		}
		return fmt.Errorf("PostgreSQL %s did not start: %w", MajorOf(id), err)
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
		var others []string
		for other := range installed {
			if other != major {
				others = append(others, other)
			}
		}
		sortMajors(others)
		next := ""
		if len(others) > 0 {
			next = installed[others[0]]
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

// prepare makes sure id's major has a cluster, so a failing initdb is reported to the caller
// instead of only as a daemon warning.
func (m *Manager) prepare(id string) error {
	return InitDB(m.Installer.BinDir(id), DataDir(m.root(), MajorOf(id)))
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
