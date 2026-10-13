//go:build !windows

package cli

import "syscall"

// execReplace replaces the flock process with the tool, so signals, TTY and exit codes behave as if it ran directly.
func execReplace(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
