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
