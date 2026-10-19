//go:build unix && !darwin

package ports

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// listeners asks /proc first: lsof isn't installed everywhere on Linux (containers, minimal
// systems), and without it Flock's own daemon would look like another program.
func listeners(port int) []listener {
	if found := procListeners(port); len(found) > 0 {
		return found
	}
	return lsofListeners(port)
}

// procListeners finds the listening TCP sockets on port in /proc/net and the processes holding
// them through /proc/<pid>/fd. Like lsof, it only sees the user's own processes without root; a
// socket whose process it can't see is still reported, without a PID.
func procListeners(port int) []listener {
	inodes := map[string]bool{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if err != nil {
			continue
		}
		lines := bufio.NewScanner(f)
		lines.Scan() // the header
		for lines.Scan() {
			// sl local_address rem_address st ... inode: "0: 0100007F:1F90 00000000:0000 0A ..."
			fields := strings.Fields(lines.Text())
			if len(fields) < 10 || fields[3] != "0A" { // 0A: LISTEN
				continue
			}
			_, hexPort, _ := strings.Cut(fields[1], ":")
			if p, err := strconv.ParseInt(hexPort, 16, 32); err == nil && int(p) == port {
				inodes[fields[9]] = true
			}
		}
		f.Close()
	}
	if len(inodes) == 0 {
		return nil
	}

	var found []listener
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	for _, fd := range fds {
		target, err := os.Readlink(fd)
		inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
		if err != nil || !inodes[inode] {
			continue
		}
		delete(inodes, inode)
		pid, _ := strconv.Atoi(strings.Split(fd, "/")[2])
		name, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
		found = append(found, listener{PID: pid, Name: strings.TrimSpace(string(name))})
	}
	for range inodes {
		found = append(found, listener{}) // another user's process
	}
	return found
}

func executable(pid int) string {
	path, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	return path
}

func special(h Holder) Holder { return h }

func homeDir() string {
	home, _ := os.UserHomeDir()
	return home
}
