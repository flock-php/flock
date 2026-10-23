// Package paths resolves where Flock keeps its config, data and sockets on each OS.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// Root returns Flock's data directory:
// ~/Library/Application Support/Flock on macOS, %AppData%\Flock on Windows.
// FLOCK_HOME overrides it (used by tests and for running isolated instances).
func Root() (string, error) {
	if dir := os.Getenv("FLOCK_HOME"); dir != "" {
		return dir, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}

	return filepath.Join(base, "Flock"), nil
}

// Socket is the daemon's API socket. AF_UNIX works on macOS and Windows 10+.
func Socket(root string) string {
	return filepath.Join(root, "flockd.sock")
}

// Run is the directory for the services' unix sockets (PHP-FPM, MySQL/MariaDB, Postgres). Kept
// short: AF_UNIX paths are limited to ~104 bytes on macOS.
func Run(root string) string {
	return filepath.Join(root, "run")
}

// EnsureRun creates Run(root) and returns it. Every process that puts a socket there calls it
// before starting: nothing else creates the directory.
func EnsureRun(root string) (string, error) {
	dir := Run(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
