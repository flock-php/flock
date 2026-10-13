//go:build !windows

package phpbuild

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// buildCommand runs the script at low priority in its own process group, so cancelling
// kills the whole tree (spc, make, clang …), not just bash.
func buildCommand(ctx context.Context, script, minor, workspace string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "nice", "-n", "10", "bash", script, minor)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "FLOCK_BUILD_DIR="+workspace)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	return cmd
}
