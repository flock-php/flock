// Package paths resolves where Flock keeps its config, data and sockets on each OS.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Root returns Flock's data directory: ~/Library/Application Support/Flock on macOS,
// %AppData%\Flock on Windows, $XDG_DATA_HOME/flock (~/.local/share/flock) on Linux.
// FLOCK_HOME overrides it (used by tests, for running isolated instances, and by the Linux
// service, which systemd starts without the user's environment).
func Root() (string, error) {
	if dir := os.Getenv("FLOCK_HOME"); dir != "" {
		return dir, nil
	}
	if runtime.GOOS == "linux" {
		return dataHome()
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}

	return filepath.Join(base, "Flock"), nil
}

// dataHome is $XDG_DATA_HOME/flock, ~/.local/share/flock when it is unset or relative (which
// the XDG spec says to ignore).
func dataHome() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, "flock"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", "flock"), nil
}

// Socket is the daemon's API socket. AF_UNIX works on macOS, Linux and Windows 10+.
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
