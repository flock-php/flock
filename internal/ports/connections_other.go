//go:build !linux

package ports

// kernelConnections is Linux's /proc; elsewhere netstat is asked.
func kernelConnections(int) (int, bool) { return 0, false }
