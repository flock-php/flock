//go:build windows

package postgres

import "os"

// stopSignal is unused on Windows, where the supervisor kills processes.
var stopSignal os.Signal

// socketArgs disables unix sockets on Windows; clients connect over TCP.
func socketArgs(string) []string {
	return []string{"-c", "unix_socket_directories="}
}
