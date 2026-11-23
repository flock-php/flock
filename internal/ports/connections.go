package ports

import (
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Connections is how many TCP connections are open to the server listening on a loopback port: a
// browser tab's WebSocket, say. It is 0 when they can't be counted.
func Connections(port int) int {
	if n, ok := kernelConnections(port); ok {
		return n
	}
	args := []string{"-an"}
	if runtime.GOOS == "windows" {
		args = append(args, "-p", "TCP")
	}
	out, err := exec.Command("netstat", args...).Output()
	if err != nil {
		return 0
	}
	return netstatConnections(string(out), port)
}

// netstatConnections counts the established connections whose local end is port in `netstat -an`
// output: "tcp4 0 0 127.0.0.1.5173 127.0.0.1.50321 ESTABLISHED" (macOS uses a dot before the
// port), "tcp 0 0 127.0.0.1:5173 … ESTABLISHED" (Linux), "  TCP 127.0.0.1:5173 … ESTABLISHED" (Windows).
func netstatConnections(out string, port int) int {
	count := 0
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.Contains(line, "ESTABLISHED") {
			continue
		}
		local := fields[1] // Windows: "TCP local remote state"
		if strings.HasPrefix(fields[0], "tcp") {
			local = fields[3] // macOS and Linux: "tcp4 recv-q send-q local remote state"
		}
		if i := strings.LastIndexAny(local, ".:"); i >= 0 && local[i+1:] == strconv.Itoa(port) {
			count++
		}
	}
	return count
}
