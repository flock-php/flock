//go:build darwin || linux

package cli

import "os/exec"

// lookPath finds a program on the PATH.
func lookPath(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}
