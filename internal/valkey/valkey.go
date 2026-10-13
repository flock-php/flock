// Package valkey runs Valkey, the open-source Redis fork, as Flock's Redis service.
// It speaks the Redis protocol, so Laravel's phpredis/predis clients work unchanged.
package valkey

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"

	"flock/internal/fsutil"
	"flock/internal/supervisor"
)

var versionPattern = regexp.MustCompile(`v=(\d+\.\d+\.\d+)`)

// Dir is where a Valkey version is installed.
func Dir(root, version string) string { return filepath.Join(root, "services", "valkey", version) }

// Server and CLI are the binaries of a version.
func Server(root, version string) string { return filepath.Join(Dir(root, version), "valkey-server") }
func CLI(root, version string) string    { return filepath.Join(Dir(root, version), "valkey-cli") }

// DataDir is shared across versions so upgrades keep the data.
func DataDir(root string) string { return filepath.Join(root, "services", "valkey", "data") }

// InstallFrom installs valkey-server and valkey-cli from a build directory and returns the version.
func InstallFrom(root, src string) (string, error) {
	for _, bin := range []string{"valkey-server", "valkey-cli"} {
		if _, err := os.Stat(filepath.Join(src, bin)); err != nil {
			return "", fmt.Errorf("%s not found in %s", bin, src)
		}
	}

	out, err := exec.Command(filepath.Join(src, "valkey-server"), "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run valkey-server: %w", err)
	}
	m := versionPattern.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("unexpected version output %q", out)
	}
	version := string(m[1])

	return version, fsutil.InstallTree(src, Dir(root, version))
}

// Installed lists installed versions.
func Installed(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "services", "valkey"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, e := range entries {
		if _, err := os.Stat(Server(root, e.Name())); err == nil {
			versions = append(versions, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return versions, nil
}

// Spec runs Valkey on 127.0.0.1:port with snapshots in DataDir.
func Spec(root, version string, port int) (supervisor.Spec, error) {
	data := DataDir(root)
	if err := os.MkdirAll(data, 0o700); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		Name: "valkey",
		Path: Server(root, version),
		Args: []string{
			"--port", fmt.Sprint(port),
			"--bind", "127.0.0.1",
			"--dir", data,
			"--daemonize", "no",
			"--protected-mode", "yes",
			"--logfile", "",
		},
		LogFile:    filepath.Join(root, "log", "valkey.log"),
		StopSignal: syscall.SIGTERM, // valkey saves a snapshot on SIGTERM
	}, nil
}
