// Package node installs official Node.js builds into <root>/node/<version>/ and resolves
// which installed version a project should use (from .nvmrc / .node-version or a default).
package node

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// DefaultBaseURL is where official Node.js builds are published.
const DefaultBaseURL = "https://nodejs.org/dist"

// Installer downloads and unpacks Node.js versions.
type Installer struct {
	Root    string       // Flock data directory; versions land in <Root>/node/<version>
	BaseURL string       // defaults to DefaultBaseURL
	HTTP    *http.Client // defaults to http.DefaultClient
}

// Dir is where all Node versions are installed.
func Dir(root string) string {
	return filepath.Join(root, "node")
}

// Install resolves spec ("22", "22.11" or "22.11.0") against the published index and installs
// the newest match. It returns the full version and is a no-op when it is already installed.
func (i *Installer) Install(ctx context.Context, spec string) (string, error) {
	versions, err := i.remoteVersions(ctx)
	if err != nil {
		return "", err
	}

	version, ok := newestMatch(versions, spec)
	if !ok {
		return "", fmt.Errorf("no Node.js release matches %q", spec)
	}

	dest := filepath.Join(Dir(i.Root), version)
	if _, err := os.Stat(dest); err == nil {
		return version, nil
	}

	name := archiveName(version, runtime.GOOS, runtime.GOARCH)
	if name == "" {
		return "", fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	sums, err := i.fetch(ctx, "v"+version+"/SHASUMS256.txt")
	if err != nil {
		return "", err
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return "", err
	}

	archive, err := i.fetch(ctx, "v"+version+"/"+name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("checksum mismatch for %s: want %s, got %s", name, want, got)
	}

	if err := os.MkdirAll(Dir(i.Root), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(Dir(i.Root), ".tmp-"+version+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if strings.HasSuffix(name, ".zip") {
		err = extractZip(archive, tmp)
	} else {
		err = extractTarGz(archive, tmp)
	}
	if err != nil {
		return "", fmt.Errorf("extract %s: %w", name, err)
	}

	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}

	return version, nil
}

func (i *Installer) remoteVersions(ctx context.Context) ([]string, error) {
	data, err := i.fetch(ctx, "index.json")
	if err != nil {
		return nil, err
	}

	var index []struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse Node.js index: %w", err)
	}

	versions := make([]string, 0, len(index))
	for _, e := range index {
		versions = append(versions, strings.TrimPrefix(e.Version, "v"))
	}

	return versions, nil
}

func (i *Installer) fetch(ctx context.Context, path string) ([]byte, error) {
	base := i.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	client := i.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	url := strings.TrimSuffix(base, "/") + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// archiveName is the published archive for a version on a platform, or "" when unsupported.
func archiveName(version, goos, goarch string) string {
	arch := map[string]string{"arm64": "arm64", "amd64": "x64"}[goarch]
	if arch == "" {
		return ""
	}

	switch goos {
	case "darwin":
		return fmt.Sprintf("node-v%s-darwin-%s.tar.gz", version, arch)
	case "windows":
		return fmt.Sprintf("node-v%s-win-%s.zip", version, arch)
	}

	return ""
}

func checksumFor(sums []byte, name string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(sums)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}

	return "", fmt.Errorf("no checksum published for %s", name)
}

// Installed lists installed versions, newest first.
func Installed(root string) ([]string, error) {
	entries, err := os.ReadDir(Dir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if _, ok := parse(e.Name()); ok {
				versions = append(versions, e.Name())
			}
		}
	}
	sortDesc(versions)

	return versions, nil
}

// Resolve returns the bin directory and version of the newest installed version matching spec.
// On Windows the binaries live at the version root; elsewhere in bin/.
func Resolve(root, spec string) (binDir, version string, err error) {
	versions, err := Installed(root)
	if err != nil {
		return "", "", err
	}

	version, ok := newestMatch(versions, spec)
	if !ok {
		return "", "", fmt.Errorf("no installed Node.js matches %q (install it with `flock node install %s`)", spec, spec)
	}

	dir := filepath.Join(Dir(root), version)
	if runtime.GOOS != "windows" {
		dir = filepath.Join(dir, "bin")
	}

	return dir, version, nil
}

// DetectSpec walks up from dir looking for .nvmrc or .node-version and returns the version spec
// it names. Aliases like "lts/*" or "node" return "" so the caller falls back to its default.
func DetectSpec(dir string) string {
	dir = filepath.Clean(dir)
	for {
		for _, name := range []string{".nvmrc", ".node-version"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			spec := strings.TrimPrefix(strings.TrimSpace(string(data)), "v")
			if _, ok := parse(spec); !ok {
				return ""
			}
			return spec
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// parse splits a version or partial spec ("22", "22.11", "22.11.0") into numbers.
func parse(v string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) == 0 || len(parts) > 3 {
		return nil, false
	}

	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		nums[i] = n
	}

	return nums, true
}

func matches(version, spec string) bool {
	v, ok := parse(version)
	s, okSpec := parse(spec)
	if !ok || !okSpec || len(v) != 3 {
		return false
	}
	for i := range s {
		if v[i] != s[i] {
			return false
		}
	}
	return true
}

func newestMatch(versions []string, spec string) (string, bool) {
	var found []string
	for _, v := range versions {
		if matches(v, spec) {
			found = append(found, v)
		}
	}
	if len(found) == 0 {
		return "", false
	}
	sortDesc(found)
	return found[0], true
}

func sortDesc(versions []string) {
	sort.Slice(versions, func(i, j int) bool {
		a, _ := parse(versions[i])
		b, _ := parse(versions[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		return len(a) > len(b)
	})
}
