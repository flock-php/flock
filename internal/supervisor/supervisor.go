// Package supervisor keeps child processes (php-fpm, postgres, ...) running,
// restarting them with backoff when they crash and stopping them gracefully.
package supervisor

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"flock/internal/envutil"
)

// Spec describes a process to keep running. Two specs with equal fields are the same process.
type Spec struct {
	Name       string
	Path       string
	Args       []string
	Env        []string // added to the daemon's environment
	Dir        string
	LogFile    string    // stdout+stderr are appended here
	StopSignal os.Signal // graceful stop signal; ignored on Windows (processes are killed)
}

// Supervisor manages a set of named processes.
type Supervisor struct {
	mu    sync.Mutex
	procs map[string]*proc
}

type proc struct {
	spec Spec
	stop chan struct{}
	done chan struct{}

	mu       sync.Mutex
	up       bool
	restarts int
	lastErr  string
}

// ProcStatus is the health of one supervised process.
type ProcStatus struct {
	Name     string `json:"name"`
	Up       bool   `json:"up"`
	Restarts int    `json:"restarts"`
	LastErr  string `json:"last_error,omitempty"` // why it last exited, plus the tail of its log
}

// New creates an empty supervisor.
func New() *Supervisor {
	return &Supervisor{procs: map[string]*proc{}}
}

// Ensure starts spec, or restarts it when it is running with a different spec.
// It reports whether a (re)start happened.
func (s *Supervisor) Ensure(spec Spec) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p, ok := s.procs[spec.Name]; ok {
		if reflect.DeepEqual(p.spec, spec) {
			return false
		}
		p.halt()
	}

	p := &proc{spec: spec, stop: make(chan struct{}), done: make(chan struct{})}
	s.procs[spec.Name] = p
	go p.run()
	return true
}

// Running lists the names of supervised processes.
func (s *Supervisor) Running() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.procs))
	for n := range s.procs {
		names = append(names, n)
	}
	return names
}

// Status reports every supervised process, sorted by name.
func (s *Supervisor) Status() []ProcStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]ProcStatus, 0, len(s.procs))
	for name, p := range s.procs {
		p.mu.Lock()
		out = append(out, ProcStatus{Name: name, Up: p.up, Restarts: p.restarts, LastErr: p.lastErr})
		p.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// Retain stops every process whose name is not in keep.
func (s *Supervisor) Retain(keep map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, p := range s.procs {
		if !keep[name] {
			p.halt()
			delete(s.procs, name)
		}
	}
}

// StopAll stops every process and waits for them to exit.
func (s *Supervisor) StopAll() {
	s.Retain(nil)
}

func (p *proc) halt() {
	close(p.stop)
	<-p.done
}

func (p *proc) run() {
	defer close(p.done)

	backoff := time.Second
	for {
		started := time.Now()
		err := p.once()
		if err == errStopped {
			return
		}

		p.mu.Lock()
		p.up = false
		p.restarts++
		p.lastErr = fmt.Sprintf("%v%s", err, lastLine(p.spec.LogFile))
		p.mu.Unlock()

		// A process that ran for a while gets a fresh backoff.
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}

		select {
		case <-p.stop:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

var errStopped = fmt.Errorf("stopped")

func (p *proc) once() error {
	log, err := openLog(p.spec.LogFile)
	if err != nil {
		return err
	}
	defer log.Close()

	cmd := exec.Command(p.spec.Path, p.spec.Args...)
	cmd.Dir = p.spec.Dir
	cmd.Env = envutil.Merge(os.Environ(), p.spec.Env)
	cmd.Stdout = log
	cmd.Stderr = log
	configure(cmd)

	fmt.Fprintf(log, "\n[flock] %s starting %s\n", time.Now().Format(time.RFC3339), p.spec.Name)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(log, "[flock] start failed: %v\n", err)
		return err
	}
	started(cmd)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	p.mu.Lock()
	p.up = true
	p.mu.Unlock()

	select {
	case err := <-exited:
		fmt.Fprintf(log, "[flock] %s exited: %v\n", p.spec.Name, err)
		return err
	case <-p.stop:
		terminate(cmd, p.spec.StopSignal)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		fmt.Fprintf(log, "[flock] %s stopped\n", p.spec.Name)
		return errStopped
	}
}

// lastLine returns the last meaningful line the process logged (Flock's own markers skipped),
// which usually explains a crash ("could not bind ... Address already in use").
func lastLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > 8192 {
		data = data[len(data)-8192:]
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "[flock]") {
			return ": " + line
		}
	}
	return ""
}

func openLog(path string) (io.WriteCloser, error) {
	if path == "" {
		return nopCloser{io.Discard}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }
