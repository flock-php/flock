// Package capture records what PHP apps do (queries, logs, views, jobs, requests and dump()
// output) without the apps installing anything.
//
// Flock's agent (agent/agent.php) runs as auto_prepend_file next to the opentelemetry extension,
// whose hook() lets it attach to the app's own classes once they load: it wraps whatever dump
// handler the app installs and listens on Laravel's events. It sends everything as JSON lines to
// the daemon's collector (Collector), which appends dumps to the dumps file (package dumps) and
// everything else to the events file here. PHP versions without the extension keep the dump
// server (dumps only).
package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"flock/internal/dumps"
)

// Kinds are the events the agent can capture besides dumps, which are always captured.
var Kinds = []string{"query", "log", "view", "job", "request"}

// MaxEvents caps the stored history; older events are dropped.
const MaxEvents = 5000

// Event is one captured event. Fields that don't apply to its kind are empty.
type Event struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"` // one of Kinds
	Time        time.Time      `json:"time"`
	Site        string         `json:"site"`  // filled in when listed, not stored
	Group       string         `json:"group"` // shared with the dumps of the same request / CLI run / job
	Request     *dumps.Request `json:"request"`
	CommandLine *string        `json:"command_line"`
	Source      *dumps.Source  `json:"source"` // the app code that caused it (the first frame outside vendor/)

	SQL        string `json:"sql,omitempty"` // query
	Bindings   []any  `json:"bindings,omitempty"`
	Connection string `json:"connection,omitempty"` // query, job

	Level   string          `json:"level,omitempty"` // log
	Message string          `json:"message,omitempty"`
	Context json.RawMessage `json:"context,omitempty"`

	View string `json:"view,omitempty"` // view
	Path string `json:"path,omitempty"`

	Job       string `json:"job,omitempty"` // job
	Queue     string `json:"queue,omitempty"`
	State     string `json:"state,omitempty"` // processing, processed or failed
	Exception string `json:"exception,omitempty"`

	StatusCode int     `json:"status_code,omitempty"` // request
	DurationMS float64 `json:"duration_ms,omitempty"` // query, job, request
}

// Valid reports whether kinds are all known event kinds.
func Valid(kinds []string) error {
	for _, k := range kinds {
		if !slices.Contains(Kinds, k) {
			return fmt.Errorf("unknown event kind %q (known: query, log, view, job, request)", k)
		}
	}
	return nil
}

// File is the JSON-lines file events are appended to, next to the dumps.
func File(root string) string { return filepath.Join(filepath.Dir(dumps.File(root)), "events.jsonl") }

var (
	mu       sync.Mutex
	lines    = -1 // in File, -1 until counted
	sequence int
	counted  struct { // Count's cache, by the file's size and time
		size int64
		mod  time.Time
		n    int
	}
)

// Append stores events. Only the daemon appends; it trims the file when it grew well past
// MaxEvents.
func Append(root string, events ...Event) error {
	mu.Lock()
	defer mu.Unlock()

	var buf bytes.Buffer
	for _, e := range events {
		if e.Time.IsZero() {
			e.Time = time.Now()
		}
		sequence++
		e.ID = fmt.Sprintf("%d-%06d", e.Time.UnixMicro(), sequence%1000000)
		e.Site = ""
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(append(line, '\n'))
	}

	if err := os.MkdirAll(filepath.Dir(File(root)), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(File(root), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	f.Close()
	if err != nil {
		return err
	}

	if lines < 0 {
		lines = countLines(File(root))
	} else {
		lines += len(events)
	}
	if lines > MaxEvents+MaxEvents/5 {
		return trim(root)
	}
	return nil
}

// trim keeps the newest MaxEvents. mu must be held.
func trim(root string) error {
	data, err := os.ReadFile(File(root))
	if err != nil {
		return err
	}
	all := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(all) > MaxEvents {
		all = all[len(all)-MaxEvents:]
	}
	lines = len(all)
	tmp := File(root) + ".tmp"
	if err := os.WriteFile(tmp, append(bytes.Join(all, []byte("\n")), '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, File(root))
}

// List returns stored events, newest first: at most limit (all when limit <= 0), of the given
// kinds (all when none are given).
func List(root string, limit int, kinds ...string) ([]Event, error) {
	data, err := os.ReadFile(File(root))
	if errors.Is(err, os.ErrNotExist) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}

	all := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	events := []Event{}
	for i := len(all) - 1; i >= 0 && len(events) < MaxEvents; i-- {
		var e Event
		if len(all[i]) == 0 || json.Unmarshal(all[i], &e) != nil {
			continue
		}
		if len(kinds) > 0 && !slices.Contains(kinds, e.Kind) {
			continue
		}
		events = append(events, e)
		if len(events) == limit {
			break
		}
	}
	return events, nil
}

// Count returns how many events are stored, without decoding them.
func Count(root string) int {
	info, err := os.Stat(File(root))
	if err != nil {
		return 0
	}
	mu.Lock()
	defer mu.Unlock()
	if counted.size != info.Size() || !counted.mod.Equal(info.ModTime()) {
		counted.size, counted.mod, counted.n = info.Size(), info.ModTime(), countLines(File(root))
	}
	return min(counted.n, MaxEvents)
}

func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			n++
		}
	}
	return n
}

// Clear deletes all stored events.
func Clear(root string) error {
	mu.Lock()
	defer mu.Unlock()
	lines = 0
	if err := os.Remove(File(root)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
