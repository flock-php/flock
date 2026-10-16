package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"flock/internal/archive"
	"flock/internal/components"
	"flock/internal/download"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;:?]*[A-Za-z]`)

func ansi(p []byte) []byte { return ansiPattern.ReplaceAll(p, nil) }

// get fetches a small document (an index, a checksum file).
func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if strings.Contains(url, "api.github.com") {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden && strings.Contains(url, "api.github.com") {
		return nil, errors.New("GitHub's limit for anonymous requests (60 per hour) was reached — try again in an hour")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// fetchArchive downloads url into a work directory next to dir (verifying sha when given) and
// unpacks it into dir, dropping the archive's single top-level directory when it has one.
// A file that isn't an archive (a bare binary) is saved as dir/name.
func fetchArchive(ctx context.Context, url, sha, dir, name string, r *components.Reporter) error {
	work := dir + ".download"
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	file := filepath.Join(work, path.Base(strings.SplitN(url, "?", 2)[0]))
	fmt.Fprintf(r.Log, "==> Downloading %s\n", url)
	r.Phase(components.PhaseDownloading, 0)
	if err := download.File(ctx, HTTPClient, url, file, sha, r.Download()); err != nil {
		return err
	}
	if sha != "" {
		fmt.Fprintf(r.Log, "==> Verified SHA-256 %s\n", sha)
	}
	r.Phase(components.PhaseInstalling, -1)
	fmt.Fprintf(r.Log, "==> Unpacking %s\n", path.Base(file))
	return unpack(file, dir, name)
}

// downloadFile downloads url to file without a checksum (the caller verifies it).
func downloadFile(ctx context.Context, url, file string, r *components.Reporter) error {
	return download.File(ctx, HTTPClient, url, file, "", r.Download())
}

// unpack extracts a .zip, .tar.gz or .tgz into dir (see fetchArchive).
func unpack(file, dir, name string) error {
	if archive.IsArchive(file) {
		return archive.Extract(file, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Rename(file, filepath.Join(dir, name)); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(dir, name), 0o755)
}

// githubRelease is the part of a GitHub release Flock reads.
type githubRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"` // "sha256:<hex>"
	} `json:"assets"`
}

// githubReleases lists a repository's releases ("owner/name"), newest first.
func githubReleases(ctx context.Context, repo string) ([]githubRelease, error) {
	data, err := get(ctx, "https://api.github.com/repos/"+repo+"/releases?per_page=100")
	if err != nil {
		return nil, err
	}
	var releases []githubRelease
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, fmt.Errorf("parse GitHub releases: %w", err)
	}
	return releases, nil
}

// githubAsset finds a release's asset by name and its SHA-256 (from GitHub's digest).
func githubAsset(ctx context.Context, repo, tag, name string) (url, sha string, err error) {
	data, err := get(ctx, "https://api.github.com/repos/"+repo+"/releases/tags/"+tag)
	if err != nil {
		return "", "", err
	}
	var rel githubRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", "", fmt.Errorf("parse GitHub release: %w", err)
	}
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL, strings.TrimPrefix(a.Digest, "sha256:"), nil
		}
	}
	return "", "", fmt.Errorf("release %s of %s has no %s", tag, repo, name)
}

// sumFor finds the hex SHA-256 of file in a checksum list ("<hex>  <file>" lines, as written by
// sha256sum, or a lone hash).
func sumFor(data []byte, file string) (string, error) {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) == 1 && len(lines) == 1 && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
		if len(f) >= 2 && len(f[0]) == 64 && strings.TrimPrefix(path.Base(f[len(f)-1]), "*") == file {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", file)
}

// parseHashes finds the SHA-256 of file in a redis-hashes README ("hash <file> sha256 <hex> <url>").
func parseHashes(data []byte, file string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "hash" && f[1] == file && f[2] == "sha256" && len(f[3]) == 64 {
			return strings.ToLower(f[3]), nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", file)
}
