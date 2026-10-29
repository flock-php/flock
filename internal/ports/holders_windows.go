package ports

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// listeners finds the listeners with netstat; describe names them.
func listeners(port int) []listener {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil
	}
	var found []listener
	for _, pid := range listenerPIDs(string(out), port) {
		found = append(found, listener{PID: pid, Name: taskName(pid)})
	}
	return found
}

// executable asks Windows for the process' image path. Protected processes (and the kernel)
// don't say.
func executable(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// taskName names a process with tasklist, for when its path can't be read.
func taskName(pid int) string {
	if pid == 0 || pid == 4 {
		return ""
	}
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	name, _, _ := strings.Cut(strings.TrimSpace(string(out)), ",")
	name = strings.Trim(name, `"`)
	if strings.HasPrefix(name, "INFO:") {
		return ""
	}
	return name
}

// special explains PID 4, the kernel: something registered the port with http.sys.
func special(h Holder) Holder {
	if h.PID == 4 {
		h.Special = "Windows' HTTP service (http.sys, pid 4 — e.g. IIS or the World Wide Web Publishing Service)"
	}
	return h
}

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

// unseen describes a holder whose process can't be seen.
func unseen(port int) Holder { return Holder{Port: port} }
