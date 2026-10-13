//go:build !windows

package supervisor

import (
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
