//go:build !windows

package share

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// killOrphans stops the cloudflared processes a crashed daemon left running, from their pid files.
// A pid that now belongs to another program is left alone.
func killOrphans(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "share-*.pid"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		_ = os.Remove(f)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 1 {
			continue
		}
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
		if err != nil || filepath.Base(strings.TrimSpace(string(out))) != "cloudflared" {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
