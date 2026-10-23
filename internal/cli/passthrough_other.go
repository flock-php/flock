//go:build !linux

package cli

// PassThrough: only Linux has another command called flock.
func PassThrough([]string) (string, bool) { return "", false }
