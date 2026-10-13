package phpbuild

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed build.sh
var buildScript []byte // a var so tests can swap in a fake build

// CheckInterval is how often php.net is asked for new releases.
var CheckInterval = 6 * time.Hour

// JobState is the lifecycle of a build.
type JobState string

const (
	Queued    JobState = "queued"
	Running   JobState = "running"
	Succeeded JobState = "succeeded"
	Failed    JobState = "failed"
	Canceled  JobState = "canceled"
)

// Job is one build of a PHP minor version.
type Job struct {
	State       JobState   `json:"state"`
	Install     bool       `json:"install"` // install when the build succeeds
	Auto        bool       `json:"auto"`    // started by the background scheduler
	Step        string     `json:"step"`    // latest log line
	FullVersion string     `json:"full_version"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	Error       string     `json:"error"`

	cancel context.CancelFunc
}

// Version is the state of one PHP minor version for the UI.
type Version struct {
	Minor       string `json:"minor"`
	Latest      string `json:"latest"`
	Installed   string `json:"installed"`
	Supported   bool   `json:"supported"`
	Status      string `json:"status"` // installed | update | ready | likely | unsupported | failed
	Note        string `json:"note"`   // why a version is unsupported, if known
	UpdateReady bool   `json:"update_ready"`
	Job         *Job   `json:"job"`
}

// Hooks connect the manager to the rest of Flock.
type Hooks struct {
	// Installed returns installed versions as minor → full version (e.g. "8.5" → "8.5.11").
	Installed func() map[string]string
	// Install installs the build in dir (containing php, php-fpm, ext/) and applies it.
	Install func(dir string) error
	// AutoBuild reports whether background builds are enabled.
	AutoBuild func() bool
}

// Manager discovers PHP releases and runs builds one at a time in the background.
type Manager struct {
	root    string
	sources Sources
	hooks   Hooks

	mu      sync.Mutex
	jobs    map[string]*Job
	queue   []string
	wake    chan struct{}
	state   persisted
	checkMu sync.Mutex
}

// persisted survives daemon restarts in <workspace>/state.json.
type persisted struct {
	Releases     []Release         `json:"releases"`
	StaticMinors map[string]bool   `json:"static_minors"`
	CheckedAt    time.Time         `json:"checked_at"`
	CheckError   string            `json:"check_error"`
	Results      map[string]string `json:"results"` // full version → "ok" | "failed"
}

// NewManager creates a manager whose workspace is <root>/build/php.
func NewManager(root string, sources Sources, hooks Hooks) *Manager {
	m := &Manager{root: root, sources: sources, hooks: hooks, jobs: map[string]*Job{}, wake: make(chan struct{}, 1)}
	m.load()
	return m
}

func (m *Manager) workspace() string { return filepath.Join(m.root, "build", "php") }
func (m *Manager) readyDir(full string) string {
	return filepath.Join(m.workspace(), "ready", full)
}

// LogPath is the build log of a minor version.
func (m *Manager) LogPath(minor string) string {
	return filepath.Join(m.root, "log", "php-build-"+minor+".log")
}

func (m *Manager) load() {
	data, err := os.ReadFile(filepath.Join(m.workspace(), "state.json"))
	if err == nil {
		_ = json.Unmarshal(data, &m.state)
	}
	if m.state.Results == nil {
		m.state.Results = map[string]string{}
	}
}

// save must be called with m.mu held.
func (m *Manager) save() {
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(m.workspace(), 0o755)
	_ = os.WriteFile(filepath.Join(m.workspace(), "state.json"), data, 0o644)
}

// Run checks for releases periodically and processes the build queue until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	go m.worker(ctx)

	// Let the daemon settle before the first network check and background builds.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_ = m.Check(ctx)
			timer.Reset(CheckInterval)
		}
	}
}

// Check refreshes release information and schedules background builds.
func (m *Manager) Check(ctx context.Context) error {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()

	releases, err := m.sources.Releases(ctx)
	var static map[string]bool
	if err == nil {
		static, err = m.sources.StaticPHPMinors(ctx)
	}

	m.mu.Lock()
	m.state.CheckedAt = time.Now()
	if err != nil {
		m.state.CheckError = err.Error()
	} else {
		m.state.CheckError = ""
		m.state.Releases, m.state.StaticMinors = releases, static
	}
	m.save()
	m.mu.Unlock()

	if err == nil && m.hooks.AutoBuild() {
		m.scheduleAuto()
	}
	return err
}

// scheduleAuto queues builds for patch updates of installed versions and for new PHP versions.
func (m *Manager) scheduleAuto() {
	installed := m.hooks.Installed()
	newest := ""
	for minor := range installed {
		if Compare(minor, newest) > 0 {
			newest = minor
		}
	}

	for _, v := range m.Versions() {
		if v.Job != nil || v.Status == "unsupported" || v.Status == "failed" {
			continue
		}
		update := v.Status == "update" && !v.UpdateReady
		brandNew := v.Installed == "" && v.Status == "likely" && Compare(v.Minor, newest) > 0
		if update || brandNew {
			m.enqueue(v.Minor, false, true)
		}
	}
}

// Versions returns every known minor version, newest first.
func (m *Manager) Versions() []Version {
	installed := m.hooks.Installed()

	m.mu.Lock()
	defer m.mu.Unlock()

	minStatic, maxStatic := "", ""
	for minor := range m.state.StaticMinors {
		if Compare(minor, maxStatic) > 0 {
			maxStatic = minor
		}
		if minStatic == "" || Compare(minor, minStatic) < 0 {
			minStatic = minor
		}
	}

	byMinor := map[string]Release{}
	for _, r := range m.state.Releases {
		byMinor[r.Minor] = r
	}
	for minor, full := range installed {
		if _, ok := byMinor[minor]; !ok {
			byMinor[minor] = Release{Minor: minor, Latest: full}
		}
	}

	out := make([]Version, 0, len(byMinor))
	for minor, r := range byMinor {
		v := Version{Minor: minor, Latest: r.Latest, Installed: installed[minor], Supported: r.Supported}
		_, readyErr := os.Stat(m.readyDir(r.Latest))
		ready := readyErr == nil

		switch {
		case v.Installed != "" && Compare(r.Latest, v.Installed) > 0:
			v.Status, v.UpdateReady = "update", ready
		case v.Installed != "":
			v.Status = "installed"
		case ready:
			v.Status = "ready"
		case m.state.Results[r.Latest] == "failed":
			v.Status = "failed"
		case m.buildable(minor, minStatic, maxStatic):
			v.Status = "likely"
		default:
			v.Status = "unsupported"
			v.Note = unsupportedNote(minor, minStatic)
		}

		if job := m.jobs[minor]; job != nil && job.State != Succeeded && job.State != Canceled {
			copied := *job
			v.Job = &copied
		}
		out = append(out, v)
	}

	slices.SortFunc(out, func(a, b Version) int { return Compare(b.Minor, a.Minor) })
	return out
}

// buildable: static-php-cli has built this minor itself, or it lies within the range of minors
// it has built, or Flock built it before. Must hold m.mu.
func (m *Manager) buildable(minor, minStatic, maxStatic string) bool {
	if m.state.StaticMinors[minor] || (maxStatic != "" && Compare(minor, minStatic) >= 0 && Compare(minor, maxStatic) <= 0) {
		return true
	}
	for full, result := range m.state.Results {
		if result == "ok" && MinorOf(full) == minor {
			return true
		}
	}
	return false
}

func unsupportedNote(minor, minStatic string) string {
	if minStatic != "" && Compare(minor, minStatic) < 0 {
		return fmt.Sprintf("PHP %s no longer compiles with current libraries and Apple's compiler (e.g. libxml2), so it can't be built.", minor)
	}
	return fmt.Sprintf("The build tooling (static-php-cli) doesn't support PHP %s yet. Flock checks again automatically.", minor)
}

// CheckedAt returns when releases were last fetched and the error, if any.
func (m *Manager) CheckedAt() (time.Time, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.CheckedAt, m.state.CheckError
}

// Install installs the latest release of minor: immediately when a ready build exists,
// otherwise by queueing a build that installs when it finishes.
func (m *Manager) Install(minor string) error {
	latest := m.latest(minor)
	if latest != "" {
		if _, err := os.Stat(m.readyDir(latest)); err == nil {
			return m.installReady(latest)
		}
	}
	m.enqueue(minor, true, false)
	return nil
}

// Build queues a build without installing it (to verify it or prepare an instant install).
func (m *Manager) Build(minor string) {
	m.enqueue(minor, false, false)
}

// Cancel stops a queued or running build.
func (m *Manager) Cancel(minor string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job := m.jobs[minor]
	if job == nil {
		return
	}
	switch job.State {
	case Queued:
		m.queue = slices.DeleteFunc(m.queue, func(x string) bool { return x == minor })
		job.State = Canceled
	case Running:
		job.cancel()
	}
}

func (m *Manager) enqueue(minor string, install, auto bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job := m.jobs[minor]; job != nil && (job.State == Queued || job.State == Running) {
		// A user action upgrades a background build: it now installs and is no longer "auto".
		job.Install = job.Install || install
		job.Auto = job.Auto && auto
		return
	}
	m.jobs[minor] = &Job{State: Queued, Install: install, Auto: auto}
	m.queue = append(m.queue, minor)

	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) latest(minor string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.state.Releases {
		if r.Minor == minor {
			return r.Latest
		}
	}
	return ""
}

func (m *Manager) installReady(full string) error {
	if err := m.hooks.Install(m.readyDir(full)); err != nil {
		return err
	}
	// The installed copy is all that's needed now.
	return os.RemoveAll(m.readyDir(full))
}

func (m *Manager) worker(ctx context.Context) {
	for {
		m.mu.Lock()
		var minor string
		var job *Job
		if len(m.queue) > 0 {
			minor, m.queue = m.queue[0], m.queue[1:]
			job = m.jobs[minor]
		}
		m.mu.Unlock()

		if job == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
				continue
			}
		}
		m.run(ctx, minor, job)
	}
}

var (
	builtLine = regexp.MustCompile(`^FLOCK_BUILT (\S+) (.+)$`)
	ansi      = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
)

func (m *Manager) run(parent context.Context, minor string, job *Job) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	now := time.Now()
	m.mu.Lock()
	job.State, job.StartedAt, job.cancel, job.Step = Running, &now, cancel, "Starting"
	m.mu.Unlock()

	full, dir, err := m.build(ctx, minor, job)

	m.mu.Lock()
	finished := time.Now()
	job.FinishedAt = &finished
	switch {
	case ctx.Err() != nil && parent.Err() == nil:
		job.State, job.Error = Canceled, ""
	case err != nil:
		job.State, job.Error = Failed, err.Error()
		if latest := m.latestLocked(minor); latest != "" {
			m.state.Results[latest] = "failed"
		}
	default:
		job.State, job.FullVersion = Succeeded, full
		m.state.Results[full] = "ok"
	}
	m.save()
	install := job.Install
	m.mu.Unlock()

	if err != nil || ctx.Err() != nil {
		return
	}

	// Keep only the newest ready build per minor.
	if old, _ := filepath.Glob(filepath.Join(m.workspace(), "ready", minor+".*")); len(old) > 0 {
		for _, o := range old {
			_ = os.RemoveAll(o)
		}
	}
	if err := os.MkdirAll(filepath.Join(m.workspace(), "ready"), 0o755); err == nil {
		err = os.Rename(dir, m.readyDir(full))
	}
	if install {
		if err := m.installReady(full); err != nil {
			m.mu.Lock()
			job.State, job.Error = Failed, "install: "+err.Error()
			m.mu.Unlock()
		}
	}
}

func (m *Manager) latestLocked(minor string) string {
	for _, r := range m.state.Releases {
		if r.Minor == minor {
			return r.Latest
		}
	}
	return ""
}

// build runs the embedded script and returns the built version and its output directory.
func (m *Manager) build(ctx context.Context, minor string, job *Job) (string, string, error) {
	if err := exec.Command("xcode-select", "-p").Run(); err != nil {
		return "", "", errors.New("the Xcode Command Line Tools are required — run `xcode-select --install`")
	}

	ws := m.workspace()
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return "", "", err
	}
	script := filepath.Join(ws, "build.sh")
	if err := os.WriteFile(script, buildScript, 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(m.LogPath(minor)), 0o700); err != nil {
		return "", "", err
	}
	logFile, err := os.Create(m.LogPath(minor))
	if err != nil {
		return "", "", err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "[flock] building PHP %s at %s\n", minor, time.Now().Format(time.RFC3339))

	reader, writer := io.Pipe()
	cmd := buildCommand(ctx, script, minor, ws)
	cmd.Stdout, cmd.Stderr = writer, writer

	var full, dir, lastLine string
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := ansi.ReplaceAllString(scanner.Text(), "")
			fmt.Fprintln(logFile, line)
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if b := builtLine.FindStringSubmatch(trimmed); b != nil {
				full, dir = b[1], b[2]
			}
			lastLine = trimmed
			m.mu.Lock()
			job.Step = shorten(strings.TrimPrefix(trimmed, "==> "), 140)
			m.mu.Unlock()
		}
	}()

	runErr := cmd.Run()
	writer.Close()
	<-done

	if runErr != nil {
		return "", "", fmt.Errorf("build failed: %s (see the build log)", shorten(lastLine, 200))
	}
	if full == "" || dir == "" {
		return "", "", errors.New("build finished without output (see the build log)")
	}
	return full, dir, nil
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
