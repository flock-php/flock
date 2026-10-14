package catalog

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
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
	base := strings.ToLower(filepath.Base(file))
	var err error
	switch {
	case strings.HasSuffix(base, ".zip"):
		err = unzip(file, dir)
	case strings.HasSuffix(base, ".tar.gz"), strings.HasSuffix(base, ".tgz"):
		err = untar(file, dir)
	default:
		if err = os.MkdirAll(dir, 0o755); err == nil {
			err = os.Rename(file, filepath.Join(dir, name))
			if err == nil {
				err = os.Chmod(filepath.Join(dir, name), 0o755)
			}
		}
		return err
	}
	if err != nil {
		return fmt.Errorf("unpack %s: %w", filepath.Base(file), err)
	}
	return flatten(dir)
}

// flatten moves the contents of dir's only entry up when that is a directory.
func flatten(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return err
	}
	inner := filepath.Join(dir, entries[0].Name())
	tmp := dir + ".flatten"
	if err := os.Rename(inner, tmp); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}

// safeJoin joins an archive entry's name to dir, refusing names that leave it.
func safeJoin(dir, name string) (string, error) {
	target := filepath.Join(dir, filepath.FromSlash(name))
	if target != dir && !strings.HasPrefix(target, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q leaves the target directory", name)
	}
	return target, nil
}

func untar(file, dir string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dir, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeFile(target, tr, os.FileMode(h.Mode).Perm()|0o600)
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("archive entry %q links outside the archive", h.Name)
			}
			if _, err = safeJoin(dir, path.Join(path.Dir(h.Name), h.Linkname)); err != nil {
				return err
			}
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				err = os.Symlink(h.Linkname, target)
			}
		case tar.TypeLink:
			var src string
			if src, err = safeJoin(dir, h.Linkname); err == nil {
				if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
					err = os.Link(src, target)
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func unzip(file, dir string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := safeJoin(dir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode().Perm() | 0o600
		if mode&0o111 == 0 && strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
			mode |= 0o755
		}
		err = writeFile(target, rc, mode)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
