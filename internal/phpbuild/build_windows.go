//go:build windows

package phpbuild

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"flock/internal/download"
	"flock/internal/php"
)

// platformMinors: on Windows a version is installable when windows.php.net has a build of it.
// php.net announces releases a little before their Windows builds are up, so the latest version
// offered is the newest one that can actually be downloaded.
func (m *Manager) platformMinors(ctx context.Context, releases []Release) (map[string]bool, []Release, error) {
	builds, err := m.sources.WindowsBuilds(ctx)
	if err != nil {
		return nil, nil, err
	}
	minors := map[string]bool{}
	for minor := range builds {
		minors[minor] = true
	}
	for i, r := range releases {
		if b, ok := builds[r.Minor]; ok && Compare(b.Version, r.Latest) < 0 {
			releases[i].Latest = b.Version
		}
	}
	return minors, releases, nil
}

// build "builds" PHP on Windows by downloading the official build from windows.php.net (and the
// matching Xdebug DLL from xdebug.org) into a directory php.InstallFrom accepts.
func (m *Manager) build(ctx context.Context, minor string, job *Job) (string, string, error) {
	if err := os.MkdirAll(filepath.Dir(m.LogPath(minor)), 0o700); err != nil {
		return "", "", err
	}
	logFile, err := os.Create(m.LogPath(minor))
	if err != nil {
		return "", "", err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "[flock] installing PHP %s from windows.php.net at %s\n", minor, time.Now().Format(time.RFC3339))

	step := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		fmt.Fprintln(logFile, "==> "+line)
		m.mu.Lock()
		job.Step = line
		m.mu.Unlock()
	}

	step("Looking up the Windows build of PHP %s", minor)
	builds, err := m.sources.WindowsBuilds(ctx)
	if err != nil {
		fmt.Fprintln(logFile, err)
		return "", "", fmt.Errorf("could not reach windows.php.net: %w", err)
	}
	b, ok := builds[minor]
	if !ok {
		return "", "", fmt.Errorf("windows.php.net has no build of PHP %s", minor)
	}
	fmt.Fprintf(logFile, "%s (%s)\n", b.URL, b.VS)

	downloads := filepath.Join(m.workspace, "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		return "", "", err
	}
	archive := filepath.Join(downloads, path.Base(b.URL))
	defer os.Remove(archive)

	err = m.download(ctx, b.URL, archive, b.SHA256, func(done, total int64) {
		if total > 0 {
			step("Downloading PHP %s (%d of %d MB)", b.Version, done>>20, total>>20)
		} else {
			step("Downloading PHP %s (%d MB)", b.Version, done>>20)
		}
	})
	if err != nil {
		fmt.Fprintln(logFile, err)
		return "", "", err
	}

	step("Unpacking PHP %s", b.Version)
	dir := filepath.Join(m.workspace, "out", b.Version)
	if err := os.RemoveAll(dir); err != nil {
		return "", "", err
	}
	if err := php.ExtractZip(archive, dir); err != nil {
		fmt.Fprintln(logFile, err)
		return "", "", fmt.Errorf("unpack %s: %w", path.Base(b.URL), err)
	}

	// Xdebug is optional, as on macOS: new PHP versions often arrive before Xdebug supports them.
	step("Downloading Xdebug for PHP %s", minor)
	if url, err := m.sources.XdebugDLL(ctx, minor, b.VS); err != nil {
		fmt.Fprintf(logFile, "Xdebug skipped: %v\n", err)
	} else if err := m.download(ctx, url, filepath.Join(dir, "ext", "php_xdebug.dll"), "", nil); err != nil {
		fmt.Fprintf(logFile, "Xdebug skipped: %v\n", err)
	} else {
		fmt.Fprintln(logFile, url)
	}

	step("Verifying PHP %s", b.Version)
	out, err := exec.CommandContext(ctx, filepath.Join(dir, "php.exe"), "-n", "-r", "echo PHP_VERSION;").CombinedOutput()
	fmt.Fprintf(logFile, "%s\n", out)
	if err != nil {
		return "", "", fmt.Errorf("the downloaded php.exe does not run: %v — it needs the Microsoft Visual C++ Redistributable: https://aka.ms/vs/17/release/vc_redist.x64.exe", err)
	}
	if got := strings.TrimSpace(string(out)); got != b.Version {
		return "", "", fmt.Errorf("downloaded PHP reports version %q, expected %s", got, b.Version)
	}

	fmt.Fprintf(logFile, "FLOCK_BUILT %s %s\n", b.Version, dir)
	return b.Version, dir, nil
}

// download streams url to dest, verifying sha256 when given. Large files get no overall timeout
// (only the context), unlike the API requests that share Sources.HTTP.
func (m *Manager) download(ctx context.Context, url, dest, sha string, progress func(done, total int64)) error {
	client := &http.Client{}
	if m.sources.HTTP != nil {
		client.Transport = m.sources.HTTP.Transport
	}
	return download.File(ctx, client, url, dest, sha, progress)
}
