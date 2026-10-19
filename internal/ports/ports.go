// Package ports checks whether local ports are free and, when they are not, who holds them.
package ports

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

// Free reports whether a TCP listener can be opened on host:port right now. On Linux, permission
// denied counts as free: it is a port below 1024 without CAP_NET_BIND_SERVICE (which `flock
// install` gives the daemon), and says nothing about another program holding it.
func Free(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return runtime.GOOS == "linux" && errors.Is(err, syscall.EACCES)
	}
	_ = ln.Close()
	return true
}

// Holder is a program listening on a port. Every field but Port may be empty: the port is taken,
// but by whom could not be found out.
type Holder struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"` // e.g. "nginx-arm64"
	Path    string `json:"path,omitempty"`    // the executable
	// App is what the user knows the program as, from where it is installed (see appFromPath):
	// "Herd" for /Applications/Herd.app/Contents/Resources/nginx-arm64.
	App string `json:"app,omitempty"`
	// Brew is the Homebrew formula the program comes from, stopped with `brew services stop`.
	Brew string `json:"brew,omitempty"`
	// Special explains a holder that isn't a program to quit, e.g. Windows' http.sys.
	Special string `json:"special,omitempty"`
}

// String names the holder: "Herd (nginx-arm64, pid 812)", "nginx (pid 812)" or "another program".
func (h Holder) String() string {
	pid := ""
	if h.PID != 0 {
		pid = "pid " + strconv.Itoa(h.PID)
	}
	switch {
	case h.Special != "":
		return h.Special
	case h.App != "" && h.Process != "":
		return fmt.Sprintf("%s (%s)", h.App, join(h.Process, pid))
	case h.App != "":
		return fmt.Sprintf("%s (%s)", h.App, pid)
	case h.Process != "" && pid != "":
		return fmt.Sprintf("%s (%s)", h.Process, pid)
	case h.Process != "":
		return h.Process
	case pid != "":
		return "the program with " + pid
	}
	return "another program"
}

// Hint says how to free the port: "quit Herd", "run `brew services stop nginx`", "quit nginx (pid 812)".
func (h Holder) Hint() string {
	switch {
	case h.Special != "":
		return ""
	case h.Brew != "":
		return fmt.Sprintf("run `brew services stop %s`", h.Brew)
	case h.App != "":
		return "quit " + h.App
	case h.Process != "":
		return "quit " + h.String()
	}
	return ""
}

func join(parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), ", ")
}

// Holders lists the programs listening on a TCP port, on any address. It is empty when the port is
// free, and also when the listeners can't be seen (Free tells those apart).
func Holders(port int) []Holder {
	var holders []Holder
	for _, l := range listeners(port) {
		h := describe(l)
		h.Port = port
		// One program's processes (nginx' workers) or sockets (IPv4 and IPv6) count once.
		if slices.ContainsFunc(holders, func(o Holder) bool { return o.PID == h.PID || o.Path != "" && o.Path == h.Path }) {
			continue
		}
		holders = append(holders, h)
	}
	return holders
}

// Foreign lists who holds a port other than Flock itself: programs installed under root (Flock's
// data directory, where its daemon, PHP and services run from) are Flock's. When nothing can be
// seen listening but the port can't be opened either, it reports an unknown holder.
func Foreign(port int, root string) []Holder {
	all := Holders(port)
	foreign := slices.DeleteFunc(slices.Clone(all), func(h Holder) bool { return Own(h, root) })
	if len(all) == 0 && !Free(probeHost(port), port) {
		foreign = []Holder{{Port: port}}
	}
	return foreign
}

// Taken explains why a service can't use port, which another program than Flock listens on:
// "port 3306 is used by DBngin (mysqld, pid 812) — quit DBngin, or pick another port". It is ""
// when the port is free or Flock's own.
func Taken(port int, root string) string {
	holders := Foreign(port, root)
	if len(holders) == 0 {
		return ""
	}
	h := holders[0]
	how := "pick another port"
	if hint := h.Hint(); hint != "" {
		how = hint + ", or " + how
	}
	return fmt.Sprintf("port %d is used by %s — %s", port, h, how)
}

// probeHost is the address to try a port on. macOS lets users listen on ports below 1024 only
// on every address, the way the daemon does for 80 and 443.
func probeHost(port int) string {
	if runtime.GOOS == "darwin" && port < 1024 {
		return ""
	}
	return "127.0.0.1"
}

// Own reports whether a holder is one of Flock's processes.
func Own(h Holder, root string) bool {
	if h.Path != "" && root != "" && within(h.Path, root) {
		return true
	}
	name := strings.TrimSuffix(strings.ToLower(h.Process), ".exe")
	if h.Path != "" {
		name = strings.TrimSuffix(strings.ToLower(baseName(h.Path)), ".exe")
	}
	return name == "flock" || name == "flock-php" || name == "flockd"
}

// within reports whether path is inside dir, comparing case-insensitively (macOS' and Windows'
// file systems usually are) and taking / and \ alike.
func within(path, dir string) bool {
	clean := func(p string) string {
		return strings.TrimSuffix(strings.ToLower(strings.ReplaceAll(p, `\`, "/")), "/")
	}
	return strings.HasPrefix(clean(path), clean(dir)+"/")
}

// Describe explains a busy port for humans, with how to free it:
// "port 80 is used by Herd (nginx-arm64, pid 812) — quit Herd".
func Describe(port int) string {
	holders := Holders(port)
	if len(holders) == 0 {
		return fmt.Sprintf("port %d is used by another program", port)
	}
	h := holders[0]
	text := fmt.Sprintf("port %d is used by %s", port, h)
	if hint := h.Hint(); hint != "" {
		text += " — " + hint
	}
	return text
}

// listenerPIDs picks the PIDs listening on port from Windows' `netstat -ano` output.
func listenerPIDs(netstat string, port int) []int {
	suffix := ":" + strconv.Itoa(port)
	var pids []int
	for _, line := range strings.Split(netstat, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 5 && fields[0] == "TCP" && fields[3] == "LISTENING" && strings.HasSuffix(fields[1], suffix) {
			if pid, err := strconv.Atoi(fields[4]); err == nil && !slices.Contains(pids, pid) {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// listener is one socket listening on a port, as the platform's tools report it.
type listener struct {
	PID  int
	Name string // the process name, possibly shortened (netstat cuts it to 16 characters)
}

// describe fills in a holder from a listener: the executable and the app it belongs to.
func describe(l listener) Holder {
	h := Holder{PID: l.PID, Process: l.Name}
	if l.PID != 0 {
		h.Path = executable(l.PID)
	}
	if h.Path != "" {
		h.Process = baseName(h.Path)
		h.App, h.Brew = appFromPath(h.Path, homeDir())
	}
	return special(h)
}

// baseName is the last element of a slash- or backslash-separated path, on any OS.
func baseName(path string) string {
	return path[strings.LastIndexAny(path, `/\`)+1:]
}

// appFromPath names the app a program belongs to from where it is installed, without knowing
// any app by name:
//
//   - inside an app bundle: the outermost bundle (/Applications/Herd.app/Contents/... is "Herd")
//   - in Homebrew's Cellar or opt: the formula (returned as brew, the app is "Homebrew")
//   - in an app's data folder: macOS' Application Support, Windows' Program Files, AppData or
//     ProgramData, or a dot folder in the home directory (~/.config/herd is "Herd")
func appFromPath(path, home string) (app, brew string) {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })

	for _, p := range parts {
		if strings.HasSuffix(strings.ToLower(p), ".app") && len(p) > len(".app") {
			return p[:len(p)-len(".app")], ""
		}
	}

	for i, p := range parts {
		if (p == "Cellar" || p == "opt") && i > 0 && (parts[i-1] == "homebrew" || parts[i-1] == "local" || parts[i-1] == ".linuxbrew") && i+1 < len(parts)-1 {
			return "Homebrew", parts[i+1]
		}
	}

	lower := make([]string, len(parts))
	for i, p := range parts {
		lower[i] = strings.ToLower(p)
	}
	// after returns the folder that follows the folders seq, which must leave something below it.
	after := func(seq ...string) string {
		for i := 0; i+len(seq) < len(lower)-1; i++ {
			if slices.Equal(lower[i:i+len(seq)], seq) {
				return parts[i+len(seq)]
			}
		}
		return ""
	}
	for _, seq := range [][]string{
		{"library", "application support"},
		{"appdata", "local", "programs"},
		{"appdata", "roaming"},
		{"appdata", "local"},
		{"program files"},
		{"program files (x86)"},
		{"programdata"},
	} {
		if name := after(seq...); name != "" && !slices.Contains(systemFolders, strings.ToLower(name)) {
			return name, ""
		}
	}

	// A dot folder in the home directory, e.g. ~/.config/herd/bin/nginx/nginx.exe or ~/.lando/bin.
	if home != "" && within(path, home) {
		rest := strings.FieldsFunc(path[len(home):], func(r rune) bool { return r == '/' || r == '\\' })
		switch {
		case len(rest) > 2 && rest[0] == ".config":
			return capitalize(rest[1]), ""
		case len(rest) > 1 && len(rest[0]) > 1 && rest[0][0] == '.':
			return capitalize(rest[0][1:]), ""
		}
	}
	return "", ""
}

// systemFolders belong to the operating system, not to an app.
var systemFolders = []string{"microsoft", "windows", "windowsapps", "common files", "packages", "temp"}

// capitalize turns "herd" into "Herd"; names that already have capitals stay as they are.
func capitalize(name string) string {
	if name == "" || strings.ToLower(name) != name {
		return name
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
