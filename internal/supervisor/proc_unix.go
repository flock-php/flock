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

func terminate(cmd *exec.Cmd, sig os.Signal) {
	if sig == nil {
		sig = syscall.SIGTERM
	}
	_ = cmd.Process.Signal(sig)
}

// started is a no-op on unix: children live in their own process group, stopped by terminate.
func started(*exec.Cmd) {}
