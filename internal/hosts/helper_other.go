//go:build !windows

package hosts

import "errors"

// Status describes the installed hosts helper (Windows only).
type Status struct {
	Installed bool
	Running   bool
	Command   string
}

// HelperStatus reports no helper outside Windows, where DNS resolves sites instead.
func HelperStatus() Status { return Status{} }

// RunHelper only exists on Windows.
func RunHelper(string, string) error {
	return errors.New("the hosts helper only runs on Windows")
}

// Configured is always false outside Windows.
func (Status) Configured(string, string) bool { return false }
