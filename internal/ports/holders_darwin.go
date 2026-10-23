package ports

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// listeners asks lsof, which sees the user's own processes, then netstat, which also names other
// users' (root's, or dnsmasq's as nobody). netstat isn't enough on its own: macOS hides other
// processes' sockets from it depending on who started it (run by a Go program, it lists none).
// Not `-p tcp`, which prints nothing in more cases still.
func listeners(port int) []listener {
	if found := lsofListeners(port); len(found) > 0 {
		return found
	}
	out, err := exec.Command("netstat", "-anv").Output()
	if err != nil {
		return nil
	}
	return netstatListeners(string(out), port)
}

// Since macOS 13 netstat -anv prints "rxbytes txbytes rhiwat shiwat process:pid" after the state,
// before that "rhiwat shiwat pid epid".
var (
	netstatProcess = regexp.MustCompile(`LISTEN\s+\d+\s+\d+\s+\d+\s+\d+\s+(.+?):(\d+)\s`)
	netstatPID     = regexp.MustCompile(`LISTEN\s+\d+\s+\d+\s+(\d+)\s+\d+\s`)
)

// netstatListeners picks the TCP sockets listening on port from `netstat -anv` output.
func netstatListeners(out string, port int) []listener {
	suffix := "." + strconv.Itoa(port)
	var found []listener
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.HasPrefix(fields[0], "tcp") || !strings.HasSuffix(fields[3], suffix) || !strings.Contains(line, "LISTEN") {
			continue
		}
		if m := netstatProcess.FindStringSubmatch(line); m != nil {
			pid, _ := strconv.Atoi(m[2])
			found = append(found, listener{PID: pid, Name: m[1]})
		} else if m := netstatPID.FindStringSubmatch(line); m != nil {
			pid, _ := strconv.Atoi(m[1])
			found = append(found, listener{PID: pid})
		} else {
			found = append(found, listener{})
		}
	}
	return found
}

// executable is the path a process was started from. kern.procargs2 has it even when the
// process renamed itself ("nginx: worker process"), but only for the user's own processes; ps
// knows the others'.
func executable(pid int) string {
	if args, err := unix.SysctlRaw("kern.procargs2", pid); err == nil && len(args) > 4 {
		path := args[4:]
		if i := bytes.IndexByte(path, 0); i >= 0 {
			path = path[:i]
		}
		if strings.HasPrefix(string(path), "/") {
			return string(path)
		}
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if path := strings.TrimSpace(string(out)); err == nil && strings.HasPrefix(path, "/") {
		return path
	}
	return ""
}

func special(h Holder) Holder { return h }

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// unseen describes a holder whose process can't be seen.
func unseen(port int) Holder { return Holder{Port: port} }
