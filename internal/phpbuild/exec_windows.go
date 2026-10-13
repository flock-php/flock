//go:build windows

package phpbuild

import (
	"context"
	"os/exec"
)

// buildCommand: building PHP on Windows is not supported yet; the command fails immediately.
func buildCommand(ctx context.Context, _, _, _ string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/c", "echo building PHP is not supported on Windows yet & exit 1")
}
