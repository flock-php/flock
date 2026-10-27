package ports

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// procNetTCP are the kernel's tables of TCP sockets. Tests replace them.
var procNetTCP = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// listeners finds the sockets listening on port in the kernel's tables and the processes holding
// them. Only the user's own processes can be looked into: another user's listener (a web server
// started by systemd) has no PID here, see unseen.
func listeners(port int) []listener {
	inodes := listenInodes(port)
	if len(inodes) == 0 {
		return nil
	}
	var found []listener
	procs, _ := filepath.Glob("/proc/[0-9]*/fd")
	for _, fds := range procs {
		links, err := os.ReadDir(fds)
		if err != nil {
			continue
		}
		for _, l := range links {
			target, err := os.Readlink(filepath.Join(fds, l.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			if inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] {
				pid, _ := strconv.Atoi(filepath.Base(filepath.Dir(fds)))
				name, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
				found = append(found, listener{PID: pid, Name: strings.TrimSpace(string(name))})
				break
			}
		}
	}
	return found
}

// listenInodes are the inodes of the sockets listening on port, on any address.
func listenInodes(port int) map[string]bool {
	inodes := map[string]bool{}
	for _, table := range procNetTCP {
		data, err := os.ReadFile(table)
		if err != nil {
			continue
		}
		for inode := range parseListeners(string(data), port) {
			inodes[inode] = true
		}
	}
	return inodes
}

// parseListeners picks the sockets listening on port from a /proc/net/tcp table: their inodes
// and the uids that own them.
func parseListeners(table string, port int) map[string]int {
	found := map[string]int{}
	hexPort := strings.ToUpper(strconv.FormatInt(int64(port), 16))
	for _, line := range strings.Split(table, "\n")[1:] {
		f := strings.Fields(line)
		// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode; 0A is LISTEN.
		if len(f) < 10 || f[3] != "0A" {
			continue
		}
		local := f[1]
		if i := strings.LastIndex(local, ":"); i < 0 || strings.TrimLeft(local[i+1:], "0") != hexPort {
			continue
		}
		uid, _ := strconv.Atoi(f[7])
		found[f[9]] = uid
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

// portServices are the systemd services that usually hold a port Flock wants, in the names the
// distributions give them.
var portServices = map[int][]string{
	80:    {"apache2", "httpd", "nginx", "caddy", "lighttpd", "haproxy", "traefik"},
	443:   {"apache2", "httpd", "nginx", "caddy", "lighttpd", "haproxy", "traefik"},
	3306:  {"mysql", "mariadb", "mysqld"},
	5432:  {"postgresql"},
	6379:  {"redis-server", "redis", "valkey-server", "valkey"},
	27017: {"mongod"},
}

// unseen describes a holder whose process can't be seen (another user's, typically root's): the
// running systemd service that usually holds the port, if one does.
func unseen(port int) Holder {
	for _, unit := range portServices[port] {
		if exec.Command("systemctl", "is-active", "--quiet", unit+".service").Run() == nil {
			return Holder{Port: port, Process: unit, Systemd: unit}
		}
	}
	return Holder{Port: port}
}
