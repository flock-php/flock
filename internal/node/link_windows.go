//go:build windows

package node

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// symlinkDir makes link a directory junction to target: unlike symbolic links, junctions need
// neither administrator rights nor Developer Mode.
func symlinkDir(target, link string) error {
	cmd := exec.Command(os.Getenv("ComSpec"))
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: fmt.Sprintf(`/d /c mklink /J "%s" "%s"`, link, target), HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mklink /J %s: %v: %s", link, err, out)
	}
	return nil
}
