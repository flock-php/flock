//go:build windows

package postgres

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// stopSignal is unused on Windows, where stopFunc asks postgres to exit.
var stopSignal os.Signal

// socketArgs disables unix sockets on Windows; clients connect over TCP.
func socketArgs(string) []string {
	return []string{"-c", "unix_socket_directories="}
}

// stopFunc stops postgres with StopFast: killing it instead would leave a cluster that has to
// run crash recovery on the next start.
func stopFunc(binDir, dataDir string) func() error {
	return func() error { return StopFast(binDir, dataDir) }
}

// hideWindow keeps pg_ctl from opening a console window when the daemon has none.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// processAlive reports whether a process with pid is running.
func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
