package share

import (
	"os"
	"path/filepath"
)

// killOrphans only removes stale pid files: on Windows cloudflared is in the daemon's job object
// (supervisor.Started), so it can't outlive the daemon.
func killOrphans(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "share-*.pid"))
	for _, f := range files {
		_ = os.Remove(f)
	}
}
