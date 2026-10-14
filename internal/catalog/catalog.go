// Package catalog installs and runs the services Flock offers on request besides PostgreSQL and
// Valkey: MySQL, MariaDB, Redis, MongoDB, Meilisearch, Typesense and RustFS. Each one runs a
// single version (of a line the user picks, like MySQL 8.4) on its own port; installs run as
// background jobs the apps follow by polling, like components.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flock/internal/components"
	"flock/internal/config"
	"flock/internal/ports"
	"flock/internal/supervisor"
)

// CheckInterval is how old the latest versions may get before the daemon looks them up again.
var CheckInterval = 24 * time.Hour

// Errors of Manager operations; the API maps them to HTTP status codes.
var (
	ErrUnknown     = errors.New("unknown service")
	ErrUnavailable = errors.New("not available on this platform")
	ErrBusy        = errors.New("a job is already running")
	ErrInvalid     = errors.New("invalid request")
)

// Line is a release line the user picks, e.g. MySQL "8.4". Versions belong to the line they
// start with ("8.4.6"), and each line keeps its own data: newer lines can't be downgraded.
type Line struct {
	ID    string `json:"id"`
	Label string `json:"label"` // "8.4 LTS"
}

// Paths are where an installed version lives.
type Paths struct {
	Root    string // Flock's root
	Dir     string // the installed version
	Data    string // the line's data directory
	Log     string // the service's log file
	Version string
	Line    string
}

// Kind is one service of the catalog.
type Kind struct {
	ID          string
	Name        string
	Category    string
	Description string
	DefaultPort int
	Lines       []Line // newest first

	// Method is how this platform gets it (components.MethodBuild or MethodDownload), "" when it
	// can't; Unavailable then says why.
	Method      string
	Estimate    string
	Unavailable string

	// Latest looks up the newest release of each line it can (by line id).
	Latest func(ctx context.Context) (map[string]string, error)
	// Install puts version into dir (which doesn't exist yet), reporting through r.
	Install func(ctx context.Context, dir, version string, r *components.Reporter) error
	// Spec prepares the data directory (initialising it the first time) and describes the server.
	Spec func(p Paths, port int) (supervisor.Spec, error)
	// Env is the .env snippet that connects a Laravel app, URL a connection URL ("" for none).
	Env func(port int) string
	URL func(port int) string
}

// LineOf returns the line a version belongs to, "" when none.
func (k *Kind) LineOf(version string) string {
	best := ""
	for _, l := range k.Lines {
		if (version == l.ID || strings.HasPrefix(version, l.ID+".") || strings.HasPrefix(version, l.ID+"-")) && len(l.ID) > len(best) {
			best = l.ID
		}
	}
	return best
}

func (k *Kind) hasLine(id string) bool {
	return slices.ContainsFunc(k.Lines, func(l Line) bool { return l.ID == id })
}

// Dir is where version of service id is installed.
func Dir(root, id, version string) string { return filepath.Join(root, "services", id, version) }

// DataDir is the data of a line of service id.
func DataDir(root, id, line string) string { return filepath.Join(root, "services", id, "data", line) }

// LogFile is the log of service id.
func LogFile(root, id string) string { return filepath.Join(root, "log", id+".log") }

// PathsOf returns the paths of the installed version of k.
func PathsOf(root string, k *Kind, version string) Paths {
	line := k.LineOf(version)
	return Paths{Root: root, Dir: Dir(root, k.ID, version), Data: DataDir(root, k.ID, line), Log: LogFile(root, k.ID),
		Version: version, Line: line}
}

// Catalog is the state of every service (GET /v1/catalog).
type Catalog struct {
	CheckedAt  *time.Time `json:"checked_at"`
	CheckError string     `json:"check_error"`
	Services   []Entry    `json:"services"`
}

// Find returns the entry of id.
func (c Catalog) Find(id string) (Entry, bool) {
	for _, e := range c.Services {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Entry is one service's state.
type Entry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Unavailable string `json:"unavailable"` // why it isn't available here
	Method      string `json:"method"`
	Estimate    string `json:"estimate"`
	Installed   string `json:"installed"` // version, "" when not installed
	Line        string `json:"line"`      // of the installed version
	Latest      string `json:"latest"`    // newest release of Line
	Update      bool   `json:"update"`
	// Port is the configured port, or for a service that isn't installed the one Flock suggests:
	// the default unless another service has it.
	Port        int         `json:"port"`
	DefaultPort int         `json:"default_port"`
	Lines       []LineState `json:"lines"`
	Job         *Job        `json:"job"`
}

// LineState is one line with what Flock knows about it.
type LineState struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Latest    string `json:"latest"`
	Installed bool   `json:"installed"`
	HasData   bool   `json:"has_data"`
}

// Job is a running or failed install.
type Job struct {
	components.Job
	Version string `json:"version"`
	Line    string `json:"line"`
}

// Manager runs one install job per service at a time and caches the latest versions.
type Manager struct {
	root   string
	path   string // state file
	kinds  []*Kind
	mutate components.Mutate

	mu       sync.Mutex
	loaded   bool
	state    persisted
	jobs     map[string]*job
	base     context.Context
	checkMu  sync.Mutex
	checking atomic.Bool
	wg       sync.WaitGroup
}

type persisted struct {
	CheckedAt  time.Time                    `json:"checked_at"`
	CheckError string                       `json:"check_error"`
	Latest     map[string]map[string]string `json:"latest"` // id → line → version
}

type job struct {
	Job
	cancel context.CancelFunc
}

// NewManager creates a manager for kinds, keeping its state and logs under root. mutate edits
// config.json and applies it.
func NewManager(root string, kinds []*Kind, mutate components.Mutate) *Manager {
	return &Manager{
		root:   root,
		path:   filepath.Join(root, "cache", "catalog.json"),
		kinds:  kinds,
		mutate: mutate,
		jobs:   map[string]*job{},
		base:   context.Background(),
	}
}

// Kinds returns the services of the catalog.
func (m *Manager) Kinds() []*Kind { return m.kinds }

// Kind returns the service id.
func (m *Manager) Kind(id string) (*Kind, error) {
	for _, k := range m.kinds {
		if k.ID == id {
			return k, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknown, id)
}

// LogPath is the output of a service's current or last install.
func (m *Manager) LogPath(id string) string {
	return filepath.Join(m.root, "log", id+"-install.log")
}

// load must be called with m.mu held.
func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	if data, err := os.ReadFile(m.path); err == nil {
		_ = json.Unmarshal(data, &m.state)
	}
	if m.state.Latest == nil {
		m.state.Latest = map[string]map[string]string{}
	}
}

// save must be called with m.mu held.
func (m *Manager) save() {
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.path), 0o755)
	_ = os.WriteFile(m.path, data, 0o644)
}

// Catalog returns every service's state without waiting on the network; stale versions are
// looked up in the background.
func (m *Manager) Catalog() Catalog {
	if m.Stale() {
		m.CheckInBackground()
	}
	cfg, err := config.Load(m.root)
	if err != nil {
		cfg = config.Default()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	out := Catalog{CheckError: m.state.CheckError, Services: []Entry{}}
	if !m.state.CheckedAt.IsZero() {
		at := m.state.CheckedAt
		out.CheckedAt = &at
	}
	for _, k := range m.kinds {
		out.Services = append(out.Services, m.entry(k, cfg))
	}
	return out
}

// entry must be called with m.mu held.
func (m *Manager) entry(k *Kind, cfg *config.Config) Entry {
	e := Entry{
		ID: k.ID, Name: k.Name, Category: k.Category, Description: k.Description,
		Available: k.Method != "", Unavailable: k.Unavailable, Method: k.Method, Estimate: k.Estimate,
		DefaultPort: k.DefaultPort, Lines: []LineState{},
	}
	latest := m.state.Latest[k.ID]
	if x, ok := cfg.Services.Extra(k.ID); ok && installed(m.root, k.ID, x.Version) {
		e.Installed, e.Line, e.Port = x.Version, k.LineOf(x.Version), x.Port
		e.Latest = latest[e.Line]
		e.Update = e.Latest != "" && components.Newer(e.Latest, e.Installed)
	} else {
		e.Port = SuggestPort(cfg, k.ID, k.DefaultPort)
	}
	for _, l := range k.Lines {
		e.Lines = append(e.Lines, LineState{
			ID: l.ID, Label: l.Label, Latest: latest[l.ID], Installed: e.Line == l.ID,
			HasData: hasData(DataDir(m.root, k.ID, l.ID)),
		})
	}
	if j := m.jobs[k.ID]; j != nil {
		copied := j.Job
		e.Job = &copied
	}
	return e
}

// installed reports whether version of service id is on disk.
func installed(root, id, version string) bool {
	_, err := os.Stat(Dir(root, id, version))
	return err == nil
}

func hasData(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

// SuggestPort returns port, or the next one up when another Flock service is configured for it
// or something else listens on it.
func SuggestPort(cfg *config.Config, id string, port int) int {
	used := cfg.UsedPorts(id)
	for p := port; p < port+100 && p <= 65535; p++ {
		if _, taken := used[p]; !taken && ports.Free("127.0.0.1", p) {
			return p
		}
	}
	return port
}

// Stale reports whether the latest versions were never looked up or are older than CheckInterval.
func (m *Manager) Stale() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	return m.state.CheckedAt.IsZero() || time.Since(m.state.CheckedAt) > CheckInterval
}

// CheckInBackground starts a check unless one is running.
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

// Check looks up the newest versions of every available service now, all at once. Versions
// that can't be looked up keep their previous value; the errors are joined into check_error.
func (m *Manager) Check(ctx context.Context) error {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()

	type result struct {
		id     string
		latest map[string]string
		err    error
	}
	results := make(chan result)
	n := 0
	for _, k := range m.kinds {
		if k.Method == "" || k.Latest == nil {
			continue
		}
		n++
		go func() {
			latest, err := k.Latest(ctx)
			if err != nil {
				err = fmt.Errorf("%s: %w", k.Name, err)
			}
			results <- result{k.ID, latest, err}
		}()
	}
	found := map[string]map[string]string{}
	var errs []string
	for range n {
		r := <-results
		if r.err != nil {
			errs = append(errs, r.err.Error())
		}
		if len(r.latest) > 0 {
			found[r.id] = r.latest
		}
	}
	slices.Sort(errs)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	m.state.CheckedAt = time.Now().UTC()
	m.state.CheckError = strings.Join(errs, "; ")
	for id, latest := range found {
		if m.state.Latest[id] == nil {
			m.state.Latest[id] = map[string]string{}
		}
		for line, v := range latest {
			m.state.Latest[id][line] = v
		}
	}
	m.save()
	if len(errs) > 0 {
		return errors.New(m.state.CheckError)
	}
	return nil
}

// Run checks for new versions when stale (soon after start, then looked at hourly) until ctx
// ends, and then cancels running jobs.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()

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
			if m.Stale() {
				checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
				_ = m.Check(checkCtx)
				cancel()
			}
			timer.Reset(time.Hour)
		}
	}
}

// Start begins installing the newest release of line (the installed line, else the newest, when
// "") in the background, on port (the configured or suggested one when 0). Installing replaces
// the installed version; each line keeps its own data.
func (m *Manager) Start(id, line string, port int) error {
	k, err := m.Kind(id)
	if err != nil {
		return err
	}
	if k.Method == "" {
		return fmt.Errorf("%s is %w: %s", k.Name, ErrUnavailable, k.Unavailable)
	}
	cfg, err := config.Load(m.root)
	if err != nil {
		return err
	}
	current, isInstalled := cfg.Services.Extra(id)
	if line == "" {
		if isInstalled && k.LineOf(current.Version) != "" {
			line = k.LineOf(current.Version)
		} else {
			line = k.Lines[0].ID
		}
	}
	if !k.hasLine(line) {
		return fmt.Errorf("%w: %s has no line %q", ErrInvalid, k.Name, line)
	}
	if port == 0 {
		port = current.Port
		if !isInstalled || port == 0 {
			port = SuggestPort(cfg, id, k.DefaultPort)
		}
	}
	if err := CheckPort(cfg, id, port); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		return fmt.Errorf("%w for %s", ErrBusy, k.Name)
	}
	action := "install"
	if isInstalled {
		action = "update"
		if k.LineOf(current.Version) != line {
			action = "install"
		}
	}
	version := m.state.Latest[id][line]
	ctx, cancel := context.WithCancel(m.base)
	j := &job{Job: Job{Job: components.Job{Action: action, State: "running", Phase: components.PhaseDownloading, Progress: -1,
		StartedAt: time.Now().UTC()}, Version: version, Line: line}, cancel: cancel}
	m.jobs[id] = j

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, k, j, line, port)
	}()
	return nil
}

// CheckPort fails when port is outside 1024–65535 or another Flock service is configured for it.
func CheckPort(cfg *config.Config, id string, port int) error {
	if port < 1024 || port > 65535 {
		return fmt.Errorf("%w: port must be between 1024 and 65535", ErrInvalid)
	}
	if other, taken := cfg.UsedPorts(id)[port]; taken {
		return fmt.Errorf("%w: port %d is used by Flock's %s service", ErrInvalid, port, other)
	}
	return nil
}

func (m *Manager) run(ctx context.Context, k *Kind, j *job, line string, port int) {
	_ = os.MkdirAll(filepath.Dir(m.LogPath(k.ID)), 0o700)
	logFile, err := os.Create(m.LogPath(k.ID))
	var log io.Writer = io.Discard
	if err == nil {
		defer logFile.Close()
		log = &lockedWriter{w: logFile}
	}

	r := components.NewReporter(log, func(phase string, progress float64) {
		m.mu.Lock()
		j.Phase, j.Progress = phase, progress
		m.mu.Unlock()
	})

	m.mu.Lock()
	version := j.Version
	m.mu.Unlock()
	if version == "" {
		fmt.Fprintf(log, "[flock] looking up the newest %s %s\n", k.Name, line)
		var latest map[string]string
		if latest, err = k.Latest(ctx); err == nil {
			m.mu.Lock()
			m.state.Latest[k.ID] = latest
			version = latest[line]
			j.Version = version
			m.mu.Unlock()
			if version == "" {
				err = fmt.Errorf("found no %s %s release for this platform", k.Name, line)
			}
		}
	}
	if err == nil {
		fmt.Fprintf(log, "[flock] %s %s %s at %s\n", j.Action, k.Name, version, time.Now().Format(time.RFC3339))
		err = m.install(ctx, k, version, port, r)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case err != nil && ctx.Err() != nil && m.base.Err() == nil:
		fmt.Fprintln(log, "[flock] canceled")
		delete(m.jobs, k.ID)
	case err != nil:
		fmt.Fprintf(log, "[flock] failed: %v\n", err)
		j.State, j.Error = "failed", err.Error()
	default:
		fmt.Fprintf(log, "[flock] installed %s %s on port %d\n", k.Name, version, port)
		delete(m.jobs, k.ID)
	}
}

// install fetches version next to the installed ones, makes it the service and removes the others.
func (m *Manager) install(ctx context.Context, k *Kind, version string, port int, r *components.Reporter) error {
	parent := filepath.Join(m.root, "services", k.ID)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	staged := filepath.Join(tmp, "out")
	if err := k.Install(ctx, staged, version, r); err != nil {
		return err
	}
	r.Phase(components.PhaseInstalling, -1)

	// A reinstall replaces the running version: stop it first (Windows locks its files).
	dir := Dir(m.root, k.ID, version)
	if _, err := os.Stat(dir); err == nil {
		if err := m.mutate(func(cfg *config.Config) error { cfg.Services.RemoveExtra(k.ID); return nil }); err != nil {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, dir); err != nil {
		return err
	}

	err = m.mutate(func(cfg *config.Config) error {
		if err := CheckPort(cfg, k.ID, port); err != nil {
			return err
		}
		cfg.Services.SetExtra(k.ID, config.Extra{Version: version, Port: port})
		cfg.Services.SetRunning(k.ID, true)
		return nil
	})
	if err != nil {
		return err
	}
	return removeVersions(m.root, k.ID, version)
}

// removeVersions deletes the installed versions of service id except keep ("" for all).
// Data and work directories stay.
func removeVersions(root, id, keep string) error {
	entries, err := os.ReadDir(filepath.Join(root, "services", id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "data" || e.Name() == keep || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(root, "services", id, e.Name())))
	}
	return errors.Join(errs...)
}

// Cancel stops a running job; it disappears once the job has stopped.
func (m *Manager) Cancel(id string) error {
	if _, err := m.Kind(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		j.cancel()
	}
	return nil
}

// Dismiss forgets a failed job.
func (m *Manager) Dismiss(id string) error {
	if _, err := m.Kind(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil && j.State != "running" {
		delete(m.jobs, id)
	}
	return nil
}

// Uninstall stops and removes a service, and with deleteData the data of every line too.
func (m *Manager) Uninstall(id string, deleteData bool) error {
	k, err := m.Kind(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		m.mu.Unlock()
		return fmt.Errorf("%w for %s", ErrBusy, k.Name)
	}
	delete(m.jobs, id)
	m.mu.Unlock()

	if err := m.mutate(func(cfg *config.Config) error { cfg.Services.RemoveExtra(id); return nil }); err != nil {
		return err
	}
	if deleteData {
		return os.RemoveAll(filepath.Join(m.root, "services", id))
	}
	return removeVersions(m.root, id, "")
}

// Wait blocks until every job has finished (for tests and shutdown).
func (m *Manager) Wait() { m.wg.Wait() }

// Spec describes the server of installed service id on its configured port.
func Spec(root string, k *Kind, x config.Extra) (supervisor.Spec, error) {
	if !installed(root, k.ID, x.Version) {
		return supervisor.Spec{}, fmt.Errorf("version %s is not installed", x.Version)
	}
	p := PathsOf(root, k, x.Version)
	if p.Line == "" {
		return supervisor.Spec{}, fmt.Errorf("version %s is not one Flock offers", x.Version)
	}
	if err := os.MkdirAll(p.Data, 0o700); err != nil {
		return supervisor.Spec{}, err
	}
	spec, err := k.Spec(p, x.Port)
	if err != nil {
		return supervisor.Spec{}, err
	}
	spec.Name, spec.LogFile = k.ID, p.Log
	return spec, nil
}

// lockedWriter serialises writes from a build's stdout and stderr and the manager, leaving out
// terminal colour codes.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.w.Write(ansi(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// HTTPClient is used for version lookups and downloads (tests replace it).
var HTTPClient = http.DefaultClient
