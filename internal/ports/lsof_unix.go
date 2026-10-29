//go:build unix

package ports

import (
	"os/exec"
	"strconv"
	"strings"
)

// lsofListeners only sees the user's own processes without root.
func lsofListeners(port int) []listener {
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc").Output()
	if err != nil {
		return nil
	}
	var found []listener
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			pid, _ := strconv.Atoi(line[1:])
			found = append(found, listener{PID: pid})
		case strings.HasPrefix(line, "c") && len(found) > 0:
			found[len(found)-1].Name = line[1:]
		}
	}
	return found
}
