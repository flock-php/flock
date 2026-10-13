//go:build !windows

package phpbuild

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed build.sh
var buildScript []byte // a var so tests can swap in a fake build

// platformMinors: the minors static-php-cli has built are proof the tooling can build them.
func (m *Manager) platformMinors(ctx context.Context, releases []Release) (map[string]bool, []Release, error) {
	static, err := m.sources.StaticPHPMinors(ctx)
	return static, releases, err
}

// build runs the embedded script and returns the built version and its output directory.
func (m *Manager) build(ctx context.Context, minor string, job *Job) (string, string, error) {
	if err := exec.Command("xcode-select", "-p").Run(); err != nil {
		return "", "", errors.New("the Xcode Command Line Tools are required — run `xcode-select --install`")
	}

	ws := m.workspace
	if strings.ContainsAny(ws, " \t") {
		return "", "", fmt.Errorf("the build directory %q contains spaces, which static-php-cli cannot handle", ws)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		return "", "", err
	}
	script := filepath.Join(ws, "build.sh")
	if err := os.WriteFile(script, buildScript, 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(m.LogPath(minor)), 0o700); err != nil {
		return "", "", err
	}
	logFile, err := os.Create(m.LogPath(minor))
	if err != nil {
		return "", "", err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "[flock] building PHP %s at %s\n", minor, time.Now().Format(time.RFC3339))

	reader, writer := io.Pipe()
	cmd := buildCommand(ctx, script, minor, ws)
	cmd.Stdout, cmd.Stderr = writer, writer

	var full, dir, lastLine string
	githubRefused := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := ansi.ReplaceAllString(scanner.Text(), "")
			fmt.Fprintln(logFile, line)
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if b := builtLine.FindStringSubmatch(trimmed); b != nil {
				full, dir = b[1], b[2]
			}
			if strings.Contains(trimmed, "returned error: 403") {
				githubRefused = true
			}
			lastLine = trimmed
			m.mu.Lock()
			job.Step = shorten(strings.TrimPrefix(trimmed, "==> "), 140)
			m.mu.Unlock()
		}
	}()

	runErr := cmd.Run()
	writer.Close()
	<-done

	if runErr != nil {
		if githubRefused {
			return "", "", errRateLimited
		}
		return "", "", fmt.Errorf("build failed: %s (see the build log)", shorten(lastLine, 200))
	}
	if full == "" || dir == "" {
		return "", "", errors.New("build finished without output (see the build log)")
	}
	return full, dir, nil
}
