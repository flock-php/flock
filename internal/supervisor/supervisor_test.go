//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countLines(path, needle string) int {
	data, _ := os.ReadFile(path)
	return strings.Count(string(data), needle)
}

func TestRestartsCrashedProcessAndStopsIt(t *testing.T) {
	log := filepath.Join(t.TempDir(), "p.log")
	s := New()
	s.Ensure(Spec{Name: "crashy", Path: "/bin/sh", Args: []string{"-c", "echo hello; exit 1"}, LogFile: log})

	waitFor(t, func() bool { return countLines(log, "hello") >= 2 })

	st := s.Status()
	if len(st) != 1 || st[0].Restarts < 1 || !strings.Contains(st[0].LastErr, "exit status 1: hello") {
		t.Fatalf("crash not reported: %+v", st)
	}
	s.StopAll()

	if len(s.Running()) != 0 {
		t.Fatal("expected nothing running")
	}
}

func TestEnsureIsIdempotentAndRestartsOnSpecChange(t *testing.T) {
	log := filepath.Join(t.TempDir(), "p.log")
	s := New()
	defer s.StopAll()

	spec := Spec{Name: "sleeper", Path: "/bin/sh", Args: []string{"-c", "echo up; sleep 60"}, LogFile: log}
	s.Ensure(spec)
	s.Ensure(spec)
	waitFor(t, func() bool { return countLines(log, "up") == 1 })

	spec.Env = []string{"X=1"}
	s.Ensure(spec)
	waitFor(t, func() bool { return countLines(log, "up") == 2 && countLines(log, "stopped") == 1 })
}

func TestRetainStopsUnlistedProcesses(t *testing.T) {
	s := New()
	defer s.StopAll()

	s.Ensure(Spec{Name: "a", Path: "/bin/sleep", Args: []string{"60"}})
	s.Ensure(Spec{Name: "b", Path: "/bin/sleep", Args: []string{"60"}})
	s.Retain(map[string]bool{"a": true})

	if got := s.Running(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("want [a], got %v", got)
	}
}

func TestStopFuncReplacesTheSignalAndDoesNotCauseRestarts(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "p.log")
	flag := filepath.Join(dir, "stop")
	// The process ignores SIGTERM and only exits when the flag file appears.
	script := "trap '' TERM; echo up; while [ ! -e " + flag + " ]; do sleep 0.05; done; echo graceful"
	stops := 0
	spec := Spec{Name: "svc", Path: "/bin/sh", Args: []string{"-c", script}, LogFile: log,
		Stop: func() error { stops++; return os.WriteFile(flag, nil, 0o600) }}

	s := New()
	s.Ensure(spec)
	waitFor(t, func() bool { return countLines(log, "up") == 1 })

	// A new closure for the same command is the same process.
	spec.Stop = func() error { stops++; return os.WriteFile(flag, nil, 0o600) }
	if s.Ensure(spec) {
		t.Fatal("Ensure restarted a process whose spec only differs in Stop")
	}

	start := time.Now()
	s.StopAll()
	if stops != 1 || countLines(log, "graceful") != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("Stop not used: %d calls, log:\n%s", stops, readFile(log))
	}
}

func TestFailingStopFuncFallsBackToTheSignal(t *testing.T) {
	log := filepath.Join(t.TempDir(), "p.log")
	s := New()
	s.Ensure(Spec{Name: "svc", Path: "/bin/sh", Args: []string{"-c", "echo up; sleep 60"}, LogFile: log,
		Stop: func() error { return errors.New("no pipe") }})
	waitFor(t, func() bool { return countLines(log, "up") == 1 })

	start := time.Now()
	s.StopAll()
	if time.Since(start) > 5*time.Second || countLines(log, "graceful stop of svc failed: no pipe") != 1 {
		t.Fatalf("no fallback to SIGTERM, log:\n%s", readFile(log))
	}
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}
