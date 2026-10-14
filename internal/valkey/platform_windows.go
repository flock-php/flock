//go:build windows

package valkey

// StopFunc asks the server on port to save and exit with SHUTDOWN: Windows has no SIGTERM, and
// killing it would lose everything written since the last snapshot. Redis on Windows uses it.
func StopFunc(port int) func() error {
	return func() error { return Shutdown(port) }
}
