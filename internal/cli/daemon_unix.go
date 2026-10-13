//go:build !windows

package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// redirectOutput points stdout and stderr (including Go runtime panics) at f.
func redirectOutput(f *os.File) {
	_ = unix.Dup2(int(f.Fd()), 1)
	_ = unix.Dup2(int(f.Fd()), 2)
	os.Stdout, os.Stderr = f, f
}

// runWatchdog is only needed on Windows; launchd keeps the daemon alive on macOS.
func runWatchdog(*cobra.Command, string) error {
	return errors.New("--watchdog is only supported on Windows (launchd restarts the daemon on macOS)")
}
