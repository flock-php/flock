//go:build !darwin && !linux && !windows

package daemon

// staleSuffixes: outside macOS, Windows and Linux there is no provisioning to scan.
func staleSuffixes(active []string) []string { return nil }
