//go:build !windows

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
)

// configure puts the child in its own process group so terminal signals aimed at the daemon don't hit it directly.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminate(cmd *exec.Cmd, sig os.Signal, group bool) {
	if sig == nil {
		sig = syscall.SIGTERM
	}
	if s, ok := sig.(syscall.Signal); ok && group {
		// The child leads its group (configure), so -pid is the group.
		_ = syscall.Kill(-cmd.Process.Pid, s)
		return
	}
	_ = cmd.Process.Signal(sig)
}

// kill ends a process that didn't stop in time, with its group when asked.
func kill(cmd *exec.Cmd, group bool) {
	if group {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}

// groupAlive reports whether a process of cmd's group still runs.
func groupAlive(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, 0) == nil
}

// started is a no-op on unix: children live in their own process group, stopped by terminate.
func started(*exec.Cmd) {}

// ownTree and endTree are no-ops on unix: a group's process group is found from its leader's pid,
// even once the leader exited (see endGroup).
func ownTree(*exec.Cmd) {}

func endTree(*exec.Cmd) {}
