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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flock/internal/binaries"
)

// CheckInterval is how old the manifest may get before it is fetched again.
var CheckInterval = 24 * time.Hour

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

// Build is FrankenPHP for one PHP minor: what is installed and what the manifest offers.
type Build struct {
	PHP        string `json:"php"`         // the PHP minor, e.g. "8.4"
	Installed  string `json:"installed"`   // FrankenPHP version, "" when not installed
	PHPVersion string `json:"php_version"` // the embedded PHP of the installed build, e.g. "8.4.14"
	Latest     string `json:"latest"`      // newest FrankenPHP in the manifest, "" when unknown
	Update     bool   `json:"update"`      // Latest (or a newer build of it) can be installed
	Size       int64  `json:"size"`        // download size of Latest
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
	source   binaries.Source
	http     *http.Client
	platform string
	// Release stops whatever runs a build before it is replaced; Installed is called after an
	// install (the daemon reloads). Either may be nil.
	Release   func(minor string)
	Installed func(minor string)

	mu         sync.Mutex
	manifest   *binaries.Manifest
	checkedAt  time.Time
	checkError string
	jobs       map[string]*job
	base       context.Context
	checking   atomic.Bool
	wg         sync.WaitGroup
}

type job struct {
	Job
	cancel context.CancelFunc
}

// NewManager creates a manager installing under root from source.
func NewManager(root string, source binaries.Source) *Manager {
	return &Manager{
		root: root, source: source, http: &http.Client{}, platform: binaries.Platform(),
		jobs: map[string]*job{}, base: context.Background(),
	}
}

// Available reports whether FrankenPHP is published for this platform.
func (m *Manager) Available() bool { return m.platform != "" }

// LogPath is the output of a minor's current or last install.
func (m *Manager) LogPath(minor string) string {
	return filepath.Join(m.root, "log", "frankenphp"+minor+"-install.log")
}

// platformKey is how the manifest names a build: "php8.4-macos-arm64" under the FrankenPHP version.
func (m *Manager) platformKey(minor string) string { return "php" + minor + "-" + m.platform }

// latest is the newest FrankenPHP version the manifest has for minor. m.mu must be held.
func (m *Manager) latest(minor string) (string, binaries.Entry, bool) {
	if m.manifest == nil {
		return "", binaries.Entry{}, false
	}
	best, entry := "", binaries.Entry{}
	for version, platforms := range m.manifest.Packages[Package] {
		if e, ok := platforms[m.platformKey(minor)]; ok && (best == "" || compare(version, best) > 0) {
			best, entry = version, e
		}
	}
	return best, entry, best != ""
}

// minors are the PHP minors the manifest has builds for on this platform. m.mu must be held.
func (m *Manager) minors() []string {
	seen := map[string]bool{}
	if m.manifest != nil {
		suffix := "-" + m.platform
		for _, platforms := range m.manifest.Packages[Package] {
			for key := range platforms {
				if minor, ok := strings.CutSuffix(strings.TrimPrefix(key, "php"), suffix); ok && strings.HasPrefix(key, "php") && Supports(minor) {
					seen[minor] = true
				}
			}
		}
	}
	var out []string
	for minor := range seen {
		out = append(out, minor)
	}
	return out
}

// State returns every build without waiting on the network; a stale manifest is fetched in the
// background.
func (m *Manager) State() State {
	if m.Available() && m.stale() {
		m.CheckInBackground()
	}
	installed := Installed(m.root)

	m.mu.Lock()
	defer m.mu.Unlock()
	st := State{Available: m.Available(), CheckError: m.checkError, Builds: []Build{}}
	if !m.checkedAt.IsZero() {
		at := m.checkedAt
		st.CheckedAt = &at
	}
	minors := map[string]bool{}
	for _, v := range append(installed, m.minors()...) {
		minors[v] = true
	}
	for minor := range m.jobs {
		minors[minor] = true
	}
	for minor := range minors {
		b := Build{PHP: minor}
		if info, ok := InstalledInfo(m.root, minor); ok {
			b.Installed, b.PHPVersion = info.Version, info.PHP
			if b.Installed == "" {
				b.Installed = "unknown"
			}
			if v, e, ok := m.latest(minor); ok {
				b.Update = compare(v, info.Version) > 0 || (v == info.Version && e.Build > info.Build && info.Build > 0)
			}
		}
		if v, e, ok := m.latest(minor); ok {
			b.Latest, b.Size = v, e.Size
		}
		if j := m.jobs[minor]; j != nil {
			copied := j.Job
			b.Job = &copied
		}
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

func (m *Manager) stale() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checkedAt.IsZero() || time.Since(m.checkedAt) > CheckInterval
}

// CheckInBackground fetches the manifest unless a fetch is running.
func (m *Manager) CheckInBackground() {
	if !m.checking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.checking.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = m.Check(ctx)
	}()
}

// Check fetches the manifest now.
func (m *Manager) Check(ctx context.Context) error {
	if !m.Available() {
		return ErrUnsupported
	}
	manifest, err := m.source.Fetch(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkedAt = time.Now().UTC()
	if err != nil {
		m.checkError = err.Error()
		return err
	}
	m.manifest, m.checkError = manifest, ""
	return nil
}

// Run keeps the manifest fresh (soon after start, then looked at hourly) until ctx ends, and then
// cancels running jobs.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	if !m.Available() {
		return
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, j := range m.jobs {
				if j.cancel != nil {
					j.cancel()
				}
			}
			m.mu.Unlock()
			return
		case <-timer.C:
			if m.stale() && len(Installed(m.root)) > 0 {
				checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
				_ = m.Check(checkCtx)
				cancel()
			}
			timer.Reset(time.Hour)
		}
	}
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
	fmt.Fprintf(log, "[flock] looking up FrankenPHP for PHP %s in %s\n", minor, m.source.URL)
	if err := m.Check(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	version, entry, ok := m.latest(minor)
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w (PHP %s)", ErrUnavailable, minor)
	}

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

	fmt.Fprintf(log, "[flock] downloading FrankenPHP %s build %d: %s\n", version, entry.Build, entry.URL)
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

	set("installing", -1)
	got, info, err := activate(m.root, dir, entry.Build, m.Release)
	if err != nil {
		return err
	}
	if got != minor {
		_ = Uninstall(m.root, got)
		return fmt.Errorf("the package for PHP %s embeds PHP %s", minor, info.PHP)
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
