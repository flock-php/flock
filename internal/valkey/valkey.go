// Package valkey runs Valkey, the open-source Redis fork, as one of Flock's services (macOS).
// It speaks the Redis protocol, so Laravel's phpredis/predis clients work unchanged.
package valkey

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"flock/internal/fsutil"
	"flock/internal/supervisor"
)

var versionPattern = regexp.MustCompile(`v=(\d+\.\d+\.\d+)`)

// Dir is where a Valkey version is installed.
func Dir(root, version string) string { return filepath.Join(root, "services", "valkey", version) }

// Server and CLI are the binaries of a version.
func Server(root, version string) string {
	return filepath.Join(Dir(root, version), exe("valkey-server"))
}
func CLI(root, version string) string { return filepath.Join(Dir(root, version), exe("valkey-cli")) }

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// DataDir is shared across versions so upgrades keep the data.
func DataDir(root string) string { return filepath.Join(root, "services", "valkey", "data") }

// InstallFrom installs valkey-server and valkey-cli from a build directory and returns the version.
func InstallFrom(root, src string) (string, error) {
	for _, bin := range []string{exe("valkey-server"), exe("valkey-cli")} {
		if _, err := os.Stat(filepath.Join(src, bin)); err != nil {
			return "", fmt.Errorf("%s not found in %s", bin, src)
		}
	}

	out, err := exec.Command(filepath.Join(src, exe("valkey-server")), "--version").Output()
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
		Stop:       StopFunc(port),
	}, nil
}

// Shutdown sends SHUTDOWN to the server on 127.0.0.1:port, which saves a snapshot and exits. It
// returns once the server has accepted the command: it closes the connection when it exits, so
// no answer (within a short wait for an error reply) means it is shutting down.
func Shutdown(port int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte("*1\r\n$8\r\nSHUTDOWN\r\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	switch {
	case strings.HasPrefix(line, "-"):
		return fmt.Errorf("SHUTDOWN: %s", strings.TrimSpace(line[1:]))
	case err != nil:
		return nil // closed (or still saving): the server is going away
	}
	return fmt.Errorf("SHUTDOWN: unexpected reply %q", strings.TrimSpace(line))
}
