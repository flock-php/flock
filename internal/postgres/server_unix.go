//go:build !windows

package postgres

import (
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
