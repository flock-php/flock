// Package components installs and updates the optional tools Flock doesn't ship with: Valkey
// (macOS only) and the PostgreSQL client tools. On macOS they are built from source, on Windows
// downloaded. Installs run as background jobs the apps follow by polling.
package components

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Component IDs.
const (
	Valkey         = "valkey"
	PostgresClient = "postgres-client"
)

// Install methods.
const (
	MethodBuild    = "build"
	MethodDownload = "download"
)

// Job phases.
const (
	PhaseDownloading = "downloading"
	PhaseBuilding    = "building"
	PhaseInstalling  = "installing"
)

// CheckInterval is how old the latest versions may get before the daemon looks them up again.
var CheckInterval = 24 * time.Hour

// Errors of Manager operations; the API maps them to HTTP status codes.
var (
	ErrUnknown  = errors.New("unknown component")
	ErrBusy     = errors.New("a job is already running")
	ErrUpToDate = errors.New("already up to date")
	ErrNoJob    = errors.New("no job")
)

// Driver is one component: how to find its newest version and install or remove it.
type Driver struct {
	ID          string
	Name        string
	Description string
	Method      string
	Estimate    string

	// Installed returns the installed version, "" when not installed.
	Installed func() string
	// Latest looks up the newest version.
	Latest func(ctx context.Context) (string, error)
	// Install installs version (downloading or building it), applies it and reports through r.
	Install func(ctx context.Context, version string, r *Reporter) error
	// Uninstall removes the component (stopping it first when it runs).
	Uninstall func() error
}

// Components is the state of every component (GET /v1/components).
type Components struct {
	CheckedAt  *time.Time  `json:"checked_at"`
	CheckError string      `json:"check_error"`
	Components []Component `json:"components"`
}

// Component is one component's state.
type Component struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   string `json:"installed"`
	Latest      string `json:"latest"`
	Update      bool   `json:"update"`
	Method      string `json:"method"`
	Estimate    string `json:"estimate"`
	Job         *Job   `json:"job"`
}

// Find returns the component with id.
func (c Components) Find(id string) (Component, bool) {
	for _, x := range c.Components {
		if x.ID == id {
			return x, true
		}
	}
	return Component{}, false
}

// Job is a running or failed install.
type Job struct {
	Action    string    `json:"action"` // install | update
	State     string    `json:"state"`  // running | failed
	Phase     string    `json:"phase"`  // downloading | building | installing
	Progress  float64   `json:"progress"`
	Error     string    `json:"error"`
	StartedAt time.Time `json:"started_at"`
}

// Reporter is how an install reports its phase and output.
type Reporter struct {
	// Log receives the install's output (the build log).
	Log io.Writer

	set func(phase string, progress float64)
}

// NewReporter creates a reporter that writes output to log and phases to set (either may be nil).
func NewReporter(log io.Writer, set func(phase string, progress float64)) *Reporter {
	if log == nil {
		log = io.Discard
	}
	return &Reporter{Log: log, set: set}
}

// Phase sets the current phase; progress is 0..1, or -1 when indeterminate.
func (r *Reporter) Phase(phase string, progress float64) {
	if r != nil && r.set != nil {
		r.set(phase, progress)
	}
}

// Download returns a download.File progress callback reporting the phase "downloading".
func (r *Reporter) Download() func(done, total int64) {
	return func(done, total int64) {
		p := -1.0
		if total > 0 {
			p = float64(done) / float64(total)
		}
		r.Phase(PhaseDownloading, p)
	}
}

// Manager runs one install job per component at a time and caches the latest versions.
type Manager struct {
	root    string
	path    string // state file
	drivers []*Driver

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
	CheckedAt  time.Time         `json:"checked_at"`
	CheckError string            `json:"check_error"`
	Latest     map[string]string `json:"latest"`
}

type job struct {
	Job
	cancel context.CancelFunc
}

// NewManager creates a manager for drivers, keeping its state and logs under root.
func NewManager(root string, drivers []*Driver) *Manager {
	return &Manager{
		root:    root,
		path:    filepath.Join(root, "cache", "components.json"),
		drivers: drivers,
		jobs:    map[string]*job{},
		base:    context.Background(),
	}
}

// LogPath is the output of a component's current or last job.
func (m *Manager) LogPath(id string) string {
	return filepath.Join(m.root, "log", id+"-install.log")
}

func (m *Manager) driver(id string) (*Driver, error) {
	for _, d := range m.drivers {
		if d.ID == id {
			return d, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknown, id)
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
		m.state.Latest = map[string]string{}
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

// Components returns every component's state without waiting on the network; stale versions are
// looked up in the background.
func (m *Manager) Components() Components {
	if m.Stale() {
		m.CheckInBackground()
	}

	installed := map[string]string{}
	for _, d := range m.drivers {
		installed[d.ID] = d.Installed()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	out := Components{CheckError: m.state.CheckError, Components: []Component{}}
	if !m.state.CheckedAt.IsZero() {
		at := m.state.CheckedAt
		out.CheckedAt = &at
	}
	for _, d := range m.drivers {
		c := Component{
			ID: d.ID, Name: d.Name, Description: d.Description, Method: d.Method, Estimate: d.Estimate,
			Installed: installed[d.ID], Latest: m.state.Latest[d.ID],
		}
		c.Update = c.Installed != "" && c.Latest != "" && Newer(c.Latest, c.Installed)
		if j := m.jobs[d.ID]; j != nil {
			copied := j.Job
			c.Job = &copied
		}
		out.Components = append(out.Components, c)
	}
	return out
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

// Check looks up the newest version of every component now. Versions that can't be looked up
// keep their previous value; the errors are joined into check_error.
func (m *Manager) Check(ctx context.Context) error {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()

	found := map[string]string{}
	var errs []string
	for _, d := range m.drivers {
		v, err := d.Latest(ctx)
		if err != nil {
			errs = append(errs, d.Name+": "+err.Error())
			continue
		}
		found[d.ID] = v
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	m.state.CheckedAt = time.Now().UTC()
	m.state.CheckError = strings.Join(errs, "; ")
	for id, v := range found {
		m.state.Latest[id] = v
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

	timer := time.NewTimer(25 * time.Second)
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

// Start begins installing (or updating) a component in the background.
func (m *Manager) Start(id string) error {
	d, err := m.driver(id)
	if err != nil {
		return err
	}
	installed := d.Installed()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		return fmt.Errorf("%w for %s", ErrBusy, d.Name)
	}
	latest := m.state.Latest[id]
	if installed != "" && latest != "" && !Newer(latest, installed) {
		return fmt.Errorf("%s %s is %w", d.Name, installed, ErrUpToDate)
	}

	action := "install"
	if installed != "" {
		action = "update"
	}
	ctx, cancel := context.WithCancel(m.base)
	j := &job{Job: Job{Action: action, State: "running", Phase: PhaseDownloading, Progress: -1, StartedAt: time.Now().UTC()}, cancel: cancel}
	m.jobs[id] = j

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, d, j, latest)
	}()
	return nil
}

func (m *Manager) run(ctx context.Context, d *Driver, j *job, version string) {
	_ = os.MkdirAll(filepath.Dir(m.LogPath(d.ID)), 0o700)
	logFile, err := os.Create(m.LogPath(d.ID))
	var log io.Writer = io.Discard
	if err == nil {
		defer logFile.Close()
		log = &lockedWriter{w: logFile, strip: true}
	}

	r := NewReporter(log, func(phase string, progress float64) {
		m.mu.Lock()
		j.Phase, j.Progress = phase, progress
		m.mu.Unlock()
	})

	if version == "" {
		fmt.Fprintf(log, "[flock] looking up the newest %s\n", d.Name)
		version, err = d.Latest(ctx)
		if err == nil {
			m.mu.Lock()
			m.state.Latest[d.ID] = version
			m.mu.Unlock()
		}
	}
	if err == nil {
		fmt.Fprintf(log, "[flock] %s %s %s at %s\n", j.Action, d.Name, version, time.Now().Format(time.RFC3339))
		err = d.Install(ctx, version, r)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case err != nil && ctx.Err() != nil && m.base.Err() == nil:
		fmt.Fprintln(log, "[flock] canceled")
		delete(m.jobs, d.ID)
	case err != nil:
		fmt.Fprintf(log, "[flock] failed: %v\n", err)
		j.State, j.Error = "failed", err.Error()
	default:
		fmt.Fprintf(log, "[flock] installed %s %s\n", d.Name, version)
		delete(m.jobs, d.ID)
	}
}

// Cancel stops a running job; it disappears once the job has stopped.
func (m *Manager) Cancel(id string) error {
	if _, err := m.driver(id); err != nil {
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
	if _, err := m.driver(id); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil && j.State != "running" {
		delete(m.jobs, id)
	}
	return nil
}

// Uninstall removes a component; not while a job for it runs.
func (m *Manager) Uninstall(id string) error {
	d, err := m.driver(id)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if j := m.jobs[id]; j != nil && j.State == "running" {
		m.mu.Unlock()
		return fmt.Errorf("%w for %s", ErrBusy, d.Name)
	}
	delete(m.jobs, id)
	m.mu.Unlock()
	return d.Uninstall()
}

// Wait blocks until every job has finished (for tests and shutdown).
func (m *Manager) Wait() { m.wg.Wait() }

// lockedWriter serialises writes from the build's stdout and stderr and the manager. With strip,
// terminal colour codes (Valkey's make colours its output) are left out of the log.
type lockedWriter struct {
	mu    sync.Mutex
	w     io.Writer
	strip bool
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]`)

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := p
	if l.strip {
		out = ansi.ReplaceAll(p, nil)
	}
	if _, err := l.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Newer reports whether dotted version a is newer than b ("9.1.10" > "9.1.9", "18.10" > "18.9").
func Newer(a, b string) bool {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(x), len(y)); i++ {
		var p, q int
		if i < len(x) {
			p, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			q, _ = strconv.Atoi(y[i])
		}
		if p != q {
			return p > q
		}
	}
	return false
}
