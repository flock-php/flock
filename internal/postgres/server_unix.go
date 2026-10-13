//go:build !windows

package postgres

import "syscall"

// stopSignal triggers postgres' "fast" shutdown: roll back open transactions and exit.
// SIGTERM ("smart") would wait for every client, e.g. a long-running queue worker.
var stopSignal = syscall.SIGINT

func socketArgs(runDir string) []string {
	return []string{"-k", runDir}
}
