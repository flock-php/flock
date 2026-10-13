//go:build windows

package supervisor

import (
	"os"
	"os/exec"
)

func configure(*exec.Cmd) {}

// terminate kills the process; Windows has no portable graceful signal for console-less children.
func terminate(cmd *exec.Cmd, _ os.Signal) {
	_ = cmd.Process.Kill()
}
