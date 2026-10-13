// Package ports checks whether local ports are free and, when they are not, who holds them.
package ports

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Free reports whether a TCP listener can be opened on host:port right now.
func Free(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// Owner names the process listening on a TCP port, e.g. "nginx (pid 812)".
// It returns "" when that cannot be determined (e.g. a root-owned process seen by a normal user).
func Owner(port int) string {
	switch runtime.GOOS {
	case "darwin", "linux":
		out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc").Output()
		if err != nil {
			return ""
		}
		var pid, name string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "p") && pid == "" {
				pid = line[1:]
			}
			if strings.HasPrefix(line, "c") && name == "" {
				name = line[1:]
			}
		}
		if name == "" {
			return ""
		}
		return fmt.Sprintf("%s (pid %s)", name, pid)
	case "windows":
		return windowsOwner(port)
	}
	return ""
}

// windowsOwner finds the listener with netstat and names it with tasklist.
func windowsOwner(port int) string {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return ""
	}
	pid := listenerPID(string(out), port)
	if pid == "" {
		return ""
	}
	if pid == "4" {
		// PID 4 is the kernel: something registered the port with http.sys (IIS, Skype, WinRM, ...).
		return "Windows' HTTP service (http.sys, pid 4 — e.g. IIS or the World Wide Web Publishing Service)"
	}

	out, err = exec.Command("tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return "pid " + pid
	}
	name, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	name = strings.Trim(name, `"`)
	if name == "" || strings.HasPrefix(name, "INFO:") {
		return "pid " + pid
	}
	return fmt.Sprintf("%s (pid %s)", name, pid)
}

// listenerPID picks the PID listening on port from `netstat -ano` output.
func listenerPID(netstat string, port int) string {
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(netstat, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == "TCP" && fields[3] == "LISTENING" && strings.HasSuffix(fields[1], suffix) {
			return fields[4]
		}
	}
	return ""
}

// Describe explains a busy port for humans.
func Describe(port int) string {
	if owner := Owner(port); owner != "" {
		return fmt.Sprintf("port %d is used by %s", port, owner)
	}
	return fmt.Sprintf("port %d is used by another program", port)
}
