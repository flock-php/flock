//go:build !windows

package components

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"flock/internal/download"
)

//go:embed valkey.sh
var valkeyScript []byte

// requireCLT fails unless the Xcode Command Line Tools (clang, make) are installed.
var requireCLT = func() error {
	if err := exec.Command("xcode-select", "-p").Run(); err != nil {
		return errors.New("the Xcode Command Line Tools are required to build from source — run `xcode-select --install`")
	}
	return nil
}

// BuildValkey downloads, verifies and compiles Valkey version in workspace. It returns the
// directory with valkey-server and valkey-cli, and a cleanup that removes the work directory.
func BuildValkey(ctx context.Context, src Sources, workspace, version string, r *Reporter) (string, func(), error) {
	r.Phase(PhaseDownloading, -1)
	sha, err := src.ValkeySHA256(ctx, version)
	if err != nil {
		return "", nil, err
	}
	return build(ctx, workspace, "valkey-"+version, fmt.Sprintf(src.ValkeySource, version), "valkey-"+version+".tar.gz",
		sha, src, valkeyScript, version, r)
}

func build(ctx context.Context, workspace, name, url, file, sha string, src Sources, script []byte, version string, r *Reporter) (string, func(), error) {
	if err := requireCLT(); err != nil {
		return "", nil, err
	}
	if strings.ContainsAny(workspace, " \t") {
		return "", nil, fmt.Errorf("the build directory %q contains spaces, which the build can't handle", workspace)
	}

	dir := filepath.Join(workspace, name)
	cleanup := func() { _ = os.RemoveAll(dir) }
	cleanup()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}

	tarball := filepath.Join(dir, file)
	fmt.Fprintf(r.Log, "==> Downloading %s\n", url)
	if err := download.File(ctx, src.Client, url, tarball, sha, r.Download()); err != nil {
		cleanup()
		return "", nil, err
	}
	fmt.Fprintf(r.Log, "==> Verified SHA-256 %s\n", sha)

	scriptPath := filepath.Join(dir, "build.sh")
	if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
		cleanup()
		return "", nil, err
	}

	r.Phase(PhaseBuilding, -1)
	out := filepath.Join(dir, "out")
	cmd := exec.CommandContext(ctx, "nice", "-n", "10", "bash", scriptPath, tarball, version, out)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = r.Log, r.Log
	// Its own process group, so cancelling kills the whole tree (make, clang …), not just bash.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Run(); err != nil {
		cleanup()
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, fmt.Errorf("the build failed (%v) — see the build log", err)
	}
	return out, cleanup, nil
}
