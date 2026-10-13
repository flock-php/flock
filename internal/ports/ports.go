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
