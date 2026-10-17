package frankenphp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"flock/internal/binaries"
	"flock/internal/php"
)

// Errors of Manager operations; the API maps them to HTTP status codes.
var (
	ErrUnsupported = errors.New("FrankenPHP isn't available for this platform")
	ErrBusy        = errors.New("FrankenPHP is already being installed for this PHP version")
	ErrUnavailable = errors.New("no FrankenPHP build is published for this PHP version")
)

// Job is a running or failed install.
type Job struct {
	Action    string    `json:"action"` // install | update
	State     string    `json:"state"`  // running | failed
	Phase     string    `json:"phase"`  // downloading | installing
	Progress  float64   `json:"progress"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"started_at"`
}

// Build is FrankenPHP for one PHP minor: what is installed and what the channel offers. The
// manifest publishes one build per PHP release (frankenphp-<php version>); a FrankenPHP upgrade
// arrives as a higher build of it.
type Build struct {
	PHP        string `json:"php"`         // the PHP minor, e.g. "8.4"
	Installed  string `json:"installed"`   // FrankenPHP version, "" when not installed
	PHPVersion string `json:"php_version"` // the embedded PHP of the installed build, e.g. "8.4.14"
	Latest     string `json:"latest"`      // newest PHP release with a build in the channel, "" when unknown
	Update     bool   `json:"update"`      // Latest (or a newer build of it) can be installed
	Size       int64  `json:"size"`        // download size of Latest
	Prerelease bool   `json:"prerelease"`  // Latest is a pre-release (beta channel)
	Yanked     string `json:"yanked"`      // why the installed build was withdrawn, "" when it wasn't
	Job        *Job   `json:"job"`
}

// State is every build (GET /v1/frankenphp).
type State struct {
	Available  bool       `json:"available"` // FrankenPHP is published for this platform
	CheckedAt  *time.Time `json:"checked_at"`
	CheckError string     `json:"check_error"`
	Builds     []Build    `json:"builds"`
}

// Manager installs builds from the binaries manifest, one job per PHP minor at a time.
type Manager struct {
	root     string
	catalog  *binaries.Catalog
	http     *http.Client
	platform string
	// Release stops whatever runs a build before it is replaced; Installed is called after an
	// install (the daemon reloads). Either may be nil.
	Release   func(minor string)
	Installed func(minor string)

	mu   sync.Mutex
	jobs map[string]*job
	base context.Context
	wg   sync.WaitGroup
}

type job struct {
	Job
	cancel context.CancelFunc
}

// NewManager creates a manager installing under root from catalog. A withdrawn (yanked) build is
// replaced by the next good one as soon as the manifest has it.
func NewManager(root string, catalog *binaries.Catalog) *Manager {
	m := &Manager{
		root: root, catalog: catalog, http: &http.Client{}, platform: binaries.Platform(),
		jobs: map[string]*job{}, base: context.Background(),
	}
	if catalog != nil {
		catalog.OnChange(m.replaceYanked)
	}
	return m
}

// Available reports whether FrankenPHP is published for this platform.
func (m *Manager) Available() bool { return m.platform != "" && m.catalog != nil }

// LogPath is the output of a minor's current or last install.
func (m *Manager) LogPath(minor string) string {
	return filepath.Join(m.root, "log", "frankenphp"+minor+"-install.log")
}

func (m *Manager) manifest() *binaries.Manifest {
	if m.catalog == nil {
		return nil
	}
	return m.catalog.Manifest()
}

// latest is the newest PHP release of minor with a build for this platform.
func latest(manifest *binaries.Manifest, platform, minor string) (binaries.Version, bool) {
	for _, v := range manifest.Versions(Package, platform) {
		if minorOf(v.Version) == minor {
			return v, true
		}
	}
	return binaries.Version{}, false
}

// extensionPack is the extension pack for minor (published for Windows, where it is added to the
// official PHP build FrankenPHP runs on).
func extensionPack(manifest *binaries.Manifest, platform, minor string) (binaries.Entry, bool) {
	for _, v := range manifest.Versions(php.ExtensionsPackage, platform) {
		if v.Version == minor {
			return v.Entry, true
		}
	}
	return binaries.Entry{}, false
}

// release finds a PHP release of the manifest.
func release(manifest *binaries.Manifest, platform, version string) (binaries.Entry, bool) {
	for _, v := range manifest.Versions(Package, platform) {
		if v.Version == version {
			return v.Entry, true
		}
	}
	return binaries.Entry{}, false
}

// minors are the PHP minors the manifest has builds for on this platform.
func minors(manifest *binaries.Manifest, platform string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range manifest.Versions(Package, platform) {
		if minor := minorOf(v.Version); Supports(minor) && !seen[minor] {
			seen[minor] = true
			out = append(out, minor)
		}
	}
	return out
}

// build describes a minor against the manifest.
func build(manifest *binaries.Manifest, platform, root, minor string) Build {
	b := Build{PHP: minor}
	v, ok := latest(manifest, platform, minor)
	pack, hasPack := extensionPack(manifest, platform, minor)
	if ok {
		b.Latest, b.Size, b.Prerelease = v.Version, v.Size, v.Prerelease
		if hasPack {
			b.Size += pack.Size
		}
	}
	if info, installed := InstalledInfo(root, minor); installed {
		b.Installed, b.PHPVersion = info.Version, info.PHP
		if b.Installed == "" {
			b.Installed = "unknown"
		}
		if e, ok := release(manifest, platform, info.PHP); ok && info.Build > 0 {
			b.Yanked = e.YankReason(info.Build)
		}
		if ok && v.Installable() && (!hasPack || pack.Installable()) {
			// A build from a local file (build 0) is replaced by the published one.
			c := binaries.CompareVersions(v.Version, info.PHP)
			b.Update = c > 0 || (c == 0 && (info.Build == 0 || v.Build > info.Build || (hasPack && pack.Build > info.ExtensionsBuild)))
		}
	}
	return b
}

// State returns every build without waiting on the network; a stale manifest is fetched in the
// background.
func (m *Manager) State() State {
	st := State{Available: m.Available(), Builds: []Build{}}
	if !st.Available {
		return st
	}
	manifest := m.manifest()
	cs := m.catalog.Status()
	st.CheckedAt, st.CheckError = cs.CheckedAt, cs.CheckError

	all := map[string]bool{}
	for _, v := range append(Installed(m.root), minors(manifest, m.platform)...) {
		all[v] = true
	}
	m.mu.Lock()
	for minor := range m.jobs {
		all[minor] = true
	}
	m.mu.Unlock()
	for minor := range all {
		b := build(manifest, m.platform, m.root, minor)
		b.Job = m.Job(minor)
		st.Builds = append(st.Builds, b)
	}
	sort.Slice(st.Builds, func(i, j int) bool { return compare(st.Builds[i].PHP, st.Builds[j].PHP) > 0 })
	return st
}

// Job returns the install job of a minor, if there is one.
func (m *Manager) Job(minor string) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil {
		copied := j.Job
		return &copied
	}
	return nil
}

// Check fetches the manifest now.
func (m *Manager) Check(ctx context.Context) error {
	if !m.Available() {
		return ErrUnsupported
	}
	_, err := m.catalog.Check(ctx)
	return err
}

// replaceYanked installs the next good build for every installed build that was withdrawn.
func (m *Manager) replaceYanked() {
	manifest := m.manifest()
	for _, minor := range Installed(m.root) {
		if b := build(manifest, m.platform, m.root, minor); b.Yanked != "" && b.Update && !m.Busy(minor) {
			_ = m.Install(minor)
		}
	}
}

// Run cancels running jobs when ctx ends (the catalog keeps the manifest fresh).
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	<-ctx.Done()
	m.mu.Lock()
	for _, j := range m.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	m.mu.Unlock()
}

// Ensure starts installing the build for minor unless it is installed, being installed, or its
// last install failed (Install retries that).
func (m *Manager) Ensure(minor string) {
	if !m.Available() || !Supports(minor) || Binary(m.root, minor) != "" {
		return
	}
	m.mu.Lock()
	j := m.jobs[minor]
	m.mu.Unlock()
	if j == nil {
		_ = m.Install(minor)
	}
}

// Install installs or updates the build for minor in the background.
func (m *Manager) Install(minor string) error {
	if !m.Available() {
		return ErrUnsupported
	}
	if !Supports(minor) {
		return fmt.Errorf("FrankenPHP needs PHP %s or newer", MinimumMinor)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil && j.State == "running" {
		return ErrBusy
	}
	action := "install"
	if Binary(m.root, minor) != "" {
		action = "update"
	}
	ctx, cancel := context.WithCancel(m.base)
	j := &job{Job: Job{Action: action, State: "running", Phase: "downloading", Progress: -1, StartedAt: time.Now().UTC()}, cancel: cancel}
	m.jobs[minor] = j
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, minor, j)
	}()
	return nil
}

func (m *Manager) run(ctx context.Context, minor string, j *job) {
	_ = os.MkdirAll(filepath.Dir(m.LogPath(minor)), 0o700)
	var log io.Writer = io.Discard
	if f, err := os.Create(m.LogPath(minor)); err == nil {
		defer f.Close()
		log = f
	}
	set := func(phase string, progress float64) {
		m.mu.Lock()
		j.Phase, j.Progress = phase, progress
		m.mu.Unlock()
	}

	err := m.install(ctx, minor, log, set)

	m.mu.Lock()
	switch {
	case err != nil && ctx.Err() != nil && m.base.Err() == nil:
		fmt.Fprintln(log, "[flock] canceled")
		delete(m.jobs, minor)
	case err != nil:
		fmt.Fprintf(log, "[flock] failed: %v\n", err)
		j.State, j.Error = "failed", err.Error()
	default:
		delete(m.jobs, minor)
	}
	m.mu.Unlock()
	if err == nil && m.Installed != nil {
		m.Installed(minor)
	}
}

func (m *Manager) install(ctx context.Context, minor string, log io.Writer, set func(string, float64)) error {
	fmt.Fprintf(log, "[flock] looking up FrankenPHP for PHP %s in the %s channel\n", minor, m.catalog.Status().Channel)
	manifest, err := m.catalog.Check(ctx)
	if err != nil {
		return err
	}
	v, ok := latest(manifest, m.platform, minor)
	if !ok {
		return fmt.Errorf("%w (PHP %s)", ErrUnavailable, minor)
	}
	version, entry := v.Version, v.Entry

	staging, err := os.MkdirTemp(filepath.Join(m.root, "frankenphp"), ".download-")
	if err != nil {
		if err := os.MkdirAll(filepath.Join(m.root, "frankenphp"), 0o755); err != nil {
			return err
		}
		if staging, err = os.MkdirTemp(filepath.Join(m.root, "frankenphp"), ".download-"); err != nil {
			return err
		}
	}
	defer os.RemoveAll(staging)

	fmt.Fprintf(log, "[flock] downloading FrankenPHP for PHP %s, build %d: %s\n", version, entry.Build, entry.URL)
	set("downloading", 0)
	dir := filepath.Join(staging, "build")
	err = binaries.Install(ctx, m.http, entry, dir, func(done, total int64) {
		p := -1.0
		if total > 0 {
			p = float64(done) / float64(total)
		}
		set("downloading", p)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "[flock] verified SHA-256 %s\n", entry.SHA256)

	extensionsBuild := 0
	if pack, ok := extensionPack(manifest, m.platform, minor); ok {
		fmt.Fprintf(log, "[flock] downloading the extensions for PHP %s, build %d: %s\n", minor, pack.Build, pack.URL)
		packDir := filepath.Join(staging, "exts")
		if err := binaries.Install(ctx, m.http, pack, packDir, nil); err != nil {
			return err
		}
		fmt.Fprintf(log, "[flock] verified SHA-256 %s\n", pack.SHA256)
		if err := php.MergeExtensionPack(dir, packDir); err != nil {
			return err
		}
		extensionsBuild = pack.Build
	}

	set("installing", -1)
	got, info, err := activate(m.root, dir, entry.Build, extensionsBuild, m.Release)
	if err != nil {
		return err
	}
	if got != minor || info.PHP != version {
		if got != minor {
			_ = Uninstall(m.root, got)
		}
		return fmt.Errorf("the package for PHP %s embeds PHP %s", version, info.PHP)
	}
	fmt.Fprintf(log, "[flock] installed FrankenPHP %s with PHP %s\n", info.Version, info.PHP)
	return nil
}

// Cancel stops a running install.
func (m *Manager) Cancel(minor string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil && j.State == "running" {
		j.cancel()
	}
}

// Dismiss forgets a failed install.
func (m *Manager) Dismiss(minor string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[minor]; j != nil && j.State != "running" {
		delete(m.jobs, minor)
	}
}

// Busy reports whether an install for minor is running.
func (m *Manager) Busy(minor string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[minor]
	return j != nil && j.State == "running"
}

// Wait blocks until every job has finished (for tests and shutdown).
func (m *Manager) Wait() { m.wg.Wait() }
