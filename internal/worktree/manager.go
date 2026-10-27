package worktree

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// States of a setup.
const (
	Queued  = "queued"
	Running = "running"
	Ready   = "ready"
	Failed  = "failed"
)

// Status is a worktree's setup, as the API reports it.
type Status struct {
	State string `json:"state"`
	Step  string `json:"step,omitempty"`  // what a running setup does now (see the Step constants)
	Error string `json:"error,omitempty"` // why it failed
	Log   string `json:"log"`             // name for /v1/logs/services/{name}
}

// LogName is the log of a worktree's setup, for `flock logs`: by the name it is served under.
func LogName(site string) string { return "worktree-" + site }

// maxRunning is how many setups run at once: each may run composer and npm installs.
const maxRunning = 2

// Manager runs the worktrees' setups, one per worktree: a setup asked for again while one runs
// joins it. Finished ones are kept (with their state) until Forget.
type Manager struct {
	root  string
	slots chan struct{}

	mu   sync.Mutex
	jobs map[string]*job
}

type job struct {
	Status
	cancel context.CancelFunc
	done   chan struct{}
}

// NewManager creates a manager writing the setups' logs to <root>/log.
func NewManager(root string) *Manager {
	return &Manager{root: root, slots: make(chan struct{}, maxRunning), jobs: map[string]*job{}}
}

// LogPath is where a setup logs.
func (m *Manager) LogPath(site string) string {
	return filepath.Join(m.root, "log", LogName(site)+".log")
}

// Start runs setup for the worktree at path (served as site) unless it is queued or running
// already, and reports the setup's status. setup gets the log and reports each step; finished is
// called with its outcome once it ends (not when it was cancelled).
func (m *Manager) Start(path, site string, setup func(ctx context.Context, log io.Writer, step func(string)) error, finished func(error)) Status {
	path = filepath.Clean(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[path]; ok && (j.State == Queued || j.State == Running) {
		return j.Status
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{Status: Status{State: Queued, Log: LogName(site)}, cancel: cancel, done: make(chan struct{})}
	m.jobs[path] = j
	go m.run(ctx, j, site, setup, finished)
	return j.Status
}

func (m *Manager) run(ctx context.Context, j *job, site string, setup func(context.Context, io.Writer, func(string)) error, finished func(error)) {
	defer close(j.done)
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		return
	}
	// The log starts over before the setup counts as running, so a follower never reads the last one.
	err := os.MkdirAll(filepath.Join(m.root, "log"), 0o755)
	var log *os.File
	if err == nil {
		log, err = os.Create(m.LogPath(site))
	}
	m.update(j, func(j *job) { j.State = Running })
	if err == nil {
		defer log.Close()
		fmt.Fprintf(log, "[flock] %s setting up %s\n", time.Now().Format(time.RFC3339), site)
		err = setup(ctx, log, func(step string) { m.update(j, func(j *job) { j.Step = step }) })
		if err == nil {
			fmt.Fprintln(log, "[flock] ready")
		} else {
			fmt.Fprintf(log, "[flock] failed: %v\n", err)
		}
	}
	if ctx.Err() != nil {
		return
	}
	m.update(j, func(j *job) {
		j.Step = ""
		if err != nil {
			j.State, j.Error = Failed, err.Error()
		} else {
			j.State = Ready
		}
	})
	if finished != nil {
		finished(err)
	}
}

func (m *Manager) update(j *job, f func(*job)) {
	m.mu.Lock()
	f(j)
	m.mu.Unlock()
}

// Get returns the status of the worktree's setup, nil when none ran since the daemon started.
func (m *Manager) Get(path string) *Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[filepath.Clean(path)]; ok {
		st := j.Status
		return &st
	}
	return nil
}

// Forget cancels the worktree's setup, waits for it to stop and drops its status.
func (m *Manager) Forget(path string) {
	m.mu.Lock()
	j, ok := m.jobs[filepath.Clean(path)]
	delete(m.jobs, filepath.Clean(path))
	m.mu.Unlock()
	if ok {
		j.cancel()
		<-j.done
	}
}

// Paths are the worktrees with a setup status.
func (m *Manager) Paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	paths := make([]string, 0, len(m.jobs))
	for p := range m.jobs {
		paths = append(paths, p)
	}
	return paths
}

// StopAll cancels every setup, for the daemon's shutdown.
func (m *Manager) StopAll() {
	for _, p := range m.Paths() {
		m.Forget(p)
	}
}
