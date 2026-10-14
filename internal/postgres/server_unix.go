//go:build !windows

package postgres

import (
	"errors"
	"os/exec"
	"syscall"
)

// stopSignal triggers postgres' "fast" shutdown: roll back open transactions and exit.
// SIGTERM ("smart") would wait for every client, e.g. a long-running queue worker.
var stopSignal = syscall.SIGINT

func socketArgs(runDir string) []string {
	return []string{"-k", runDir}
}

// stopFunc is nil on unix: stopSignal does the job.
func stopFunc(_, _ string) func() error { return nil }

func hideWindow(*exec.Cmd) {}

// processAlive reports whether a process with pid exists.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
