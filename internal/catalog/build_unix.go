//go:build !windows

package catalog

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"flock/internal/components"
	"flock/internal/download"
)

var (
	//go:embed redis.sh
	redisScript []byte
	//go:embed mariadb.sh
	mariadbScript []byte
)

// The build tools MariaDB needs that macOS doesn't have, pinned (Kitware's and OpenSSL's
// published SHA-256s). Built and unpacked once into the build workspace.
const (
	cmakeVersion   = "4.4.4"
	cmakeURL       = "https://github.com/Kitware/CMake/releases/download/v4.4.4/cmake-4.4.4-macos-universal.tar.gz"
	cmakeSHA256    = "4b7b73704b1db9b374e5c9ab8e17ac6148b817b6396ee75cd94a852cbac9d305"
	opensslVersion = "3.5.9"
	opensslURL     = "https://github.com/openssl/openssl/releases/download/openssl-3.5.9/openssl-3.5.9.tar.gz"
	opensslSHA256  = "603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a"
)

// buildRedis downloads Redis's source (checked against redis-hashes) and compiles it into dir.
func buildRedis(ctx context.Context, dir, version string, r *components.Reporter) error {
	r.Phase(components.PhaseDownloading, -1)
	data, err := get(ctx, "https://raw.githubusercontent.com/redis/redis-hashes/master/README")
	if err != nil {
		return err
	}
	file := "redis-" + version + ".tar.gz"
	sha, err := parseHashes(data, file)
	if err != nil {
		return fmt.Errorf("redis-hashes has no checksum for Redis %s yet — try again later", version)
	}
	return buildFromSource(ctx, dir, "redis-"+version, "https://download.redis.io/releases/"+file, file, sha, redisScript, version, nil, r)
}

// buildMariaDB downloads MariaDB's source (checked against downloads.mariadb.org), the build
// tools it needs, and compiles it into dir.
func buildMariaDB(ctx context.Context, dir, version string, r *components.Reporter) error {
	r.Phase(components.PhaseDownloading, -1)
	file := "mariadb-" + version + ".tar.gz"
	sha, err := mariadbSHA256(ctx, version, file)
	if err != nil {
		return err
	}
	ws, err := components.DefaultWorkspace()
	if err != nil {
		return err
	}
	tools := filepath.Join(ws, "tools")
	cmake, err := ensureCMake(ctx, tools, r)
	if err != nil {
		return err
	}
	opensslTarball := filepath.Join(tools, "openssl-"+opensslVersion+".tar.gz")
	if _, err := os.Stat(opensslTarball); err != nil {
		fmt.Fprintf(r.Log, "==> Downloading %s\n", opensslURL)
		if err := download.File(ctx, HTTPClient, opensslURL, opensslTarball, opensslSHA256, r.Download()); err != nil {
			return err
		}
	}
	env := []string{
		"CMAKE=" + cmake,
		"OPENSSL_TARBALL=" + opensslTarball,
		"OPENSSL_PREFIX=" + filepath.Join(tools, "openssl-"+opensslVersion+"-"+runtime.GOARCH),
	}
	url := "https://archive.mariadb.org/mariadb-" + version + "/source/" + file
	return buildFromSource(ctx, dir, "mariadb-"+version, url, file, sha, mariadbScript, version, env, r)
}

// ensureCMake unpacks Kitware's cmake into tools once and returns the binary.
func ensureCMake(ctx context.Context, tools string, r *components.Reporter) (string, error) {
	dir := filepath.Join(tools, "cmake-"+cmakeVersion)
	bin := filepath.Join(dir, "CMake.app", "Contents", "bin", "cmake")
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	_ = os.RemoveAll(dir)
	if err := fetchArchive(ctx, cmakeURL, cmakeSHA256, dir, "", r); err != nil {
		return "", fmt.Errorf("download cmake: %w", err)
	}
	if _, err := os.Stat(bin); err != nil {
		return "", fmt.Errorf("cmake not found in %s", filepath.Base(cmakeURL))
	}
	return bin, nil
}

var cltMissing = regexp.MustCompile(`(?i)xcrun|xcode-select|command line tools`)

// buildFromSource downloads a source tarball (verifying sha) into a work directory of the build
// workspace, runs script with <tarball> <version> <out> there and moves out to dir.
func buildFromSource(ctx context.Context, dir, name, url, file, sha string, script []byte, version string, env []string, r *components.Reporter) error {
	if err := exec.Command("xcode-select", "-p").Run(); err != nil {
		return errors.New("the Xcode Command Line Tools are required to build from source — run `xcode-select --install`")
	}
	ws, err := components.DefaultWorkspace()
	if err != nil {
		return err
	}
	if strings.ContainsAny(ws, " \t") {
		return fmt.Errorf("the build directory %q contains spaces, which the build can't handle", ws)
	}
	work := filepath.Join(ws, name)
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	tarball := filepath.Join(work, file)
	fmt.Fprintf(r.Log, "==> Downloading %s\n", url)
	if err := download.File(ctx, HTTPClient, url, tarball, sha, r.Download()); err != nil {
		return err
	}
	fmt.Fprintf(r.Log, "==> Verified SHA-256 %s\n", sha)

	scriptPath := filepath.Join(work, "build.sh")
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		return err
	}
	r.Phase(components.PhaseBuilding, -1)
	out := filepath.Join(work, "out")
	cmd := exec.CommandContext(ctx, "nice", "-n", "10", "bash", scriptPath, tarball, version, out)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = r.Log, r.Log
	// Its own process group, so cancelling kills the whole tree (make, clang …), not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("the build failed (%v) — see the build log", err)
	}
	r.Phase(components.PhaseInstalling, -1)
	return os.Rename(out, dir)
}
