//go:build !windows

package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configure puts the child in its own process group so terminal signals aimed at the daemon don't hit it directly.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func shellCommand(line string) *exec.Cmd { return exec.Command("/bin/sh", "-c", line) }

// killTree kills cmd's process group: configure made it the group's leader.
func killTree(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}

func terminate(cmd *exec.Cmd, sig os.Signal) {
	if sig == nil {
		sig = syscall.SIGTERM
	}
	_ = cmd.Process.Signal(sig)
}

// started is a no-op on unix: children live in their own process group, stopped by terminate.
func started(*exec.Cmd) {}

// suspend is a no-op on unix: the process group exists from the start, before any child.
func suspend(*exec.Cmd) {}

// tree is a child's process group (configure makes it the leader of one), which its own children
// join unless they start a group of their own.
type tree struct{ pgid int }

func newTree(cmd *exec.Cmd) (*tree, error) {
	return &tree{pgid: cmd.Process.Pid}, nil
}

func (t *tree) signal(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		s = syscall.SIGTERM
	}
	_ = syscall.Kill(-t.pgid, s)
}

func (t *tree) kill() { _ = syscall.Kill(-t.pgid, syscall.SIGKILL) }

// empty reports whether no process is left in the group. Only ESRCH says so: macOS answers EPERM
// while the group's processes are exiting but not reaped yet (settle kills what's left in time).
func (t *tree) empty() bool { return errors.Is(syscall.Kill(-t.pgid, 0), syscall.ESRCH) }

func (t *tree) close() {}
