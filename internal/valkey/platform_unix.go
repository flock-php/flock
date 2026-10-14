//go:build !windows

package valkey

// StopFunc is nil on unix: SIGTERM makes the server save and exit.
func StopFunc(int) func() error { return nil }
