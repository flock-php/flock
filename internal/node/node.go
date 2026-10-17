// Package node installs Node.js from the binaries manifest into <root>/node/<version>/ and
// resolves which installed version a project should use (from .nvmrc / .node-version or a default).
package node

import (
	"context"
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

	"flock/internal/binaries"
)

// Package is Node.js' name in the binaries manifest.
const Package = "node"

// Installer installs Node.js versions from the manifest.
type Installer struct {
	Root    string            // Flock data directory; versions land in <Root>/node/<version>
	Catalog *binaries.Catalog // the manifest of the configured channel
	// NpmRegistryURL is npm's package document; defaults to npmRegistry.
	NpmRegistryURL string
	HTTP           *http.Client // defaults to http.DefaultClient
}

// Dir is where all Node versions are installed.
func Dir(root string) string {
	return filepath.Join(root, "node")
}

// published is the newest release of every major the manifest has for this platform.
func published(manifest *binaries.Manifest) map[string]binaries.Version {
	out := map[string]binaries.Version{}
	for _, v := range manifest.Versions(Package, binaries.Platform()) { // newest first
		if m := MajorOf(v.Version); m != "" {
			if _, ok := out[m]; !ok {
				out[m] = v
			}
		}
	}
	return out
}

// Install installs the newest published release of spec's major ("22", "22.11" or "22.11.0": the
// manifest has one release per major, so a pinned patch gets the newest of its major). It returns
// the version and is a no-op when it is already installed.
func (i *Installer) Install(ctx context.Context, spec string) (string, error) {
	if ValidSpec(spec) == "" {
		return "", fmt.Errorf("no Node.js release matches %q", spec)
	}
	if i.Catalog == nil {
		return "", errors.New("no package manifest")
	}
	manifest, err := i.Catalog.Check(ctx)
	if err != nil {
		return "", err
	}
	release, ok := published(manifest)[MajorOf(spec)]
	if !ok {
		return "", fmt.Errorf("no Node.js %s build is published for this platform", MajorOf(spec))
	}
	version := release.Version

	dest := filepath.Join(Dir(i.Root), version)
	if _, err := os.Stat(dest); err == nil {
		return version, nil
	}
	if err := os.MkdirAll(Dir(i.Root), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(Dir(i.Root), ".tmp-"+version+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	unpacked := filepath.Join(tmp, "node")
	if err := binaries.Install(ctx, i.client(), release.Entry, unpacked, nil); err != nil {
		return "", fmt.Errorf("Node.js %s: %w", version, err)
	}
	if err := os.Rename(unpacked, dest); err != nil {
		return "", err
	}
	return version, nil
}

func (i *Installer) client() *http.Client {
	if i.HTTP != nil {
		return i.HTTP
	}
	return http.DefaultClient
}

func (i *Installer) getAccept(ctx context.Context, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := i.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	return io.ReadAll(resp.Body)
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

// NotInstalledError reports that no installed version matches Spec.
type NotInstalledError struct{ Spec string }

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("no installed Node.js matches %q (install it with `flock node install %s`)", e.Spec, e.Spec)
}

// Resolve returns the bin directory and version of the newest installed version matching spec.
// A pinned patch ("22.11.0") that isn't installed resolves to the newest installed release of its
// major: Flock installs one release per major. On Windows the binaries live at the version root;
// elsewhere in bin/.
func Resolve(root, spec string) (binDir, version string, err error) {
	versions, err := Installed(root)
	if err != nil {
		return "", "", err
	}

	version, ok := newestMatch(versions, spec)
	if !ok {
		version, ok = newestMatch(versions, MajorOf(spec))
	}
	if !ok {
		return "", "", &NotInstalledError{Spec: spec}
	}

	dir, err := BinDir(root, version)
	return dir, version, err
}

// BinDir is where an installed version's node (and bundled npm) are: on Windows the version
// directory itself, elsewhere its bin/.
func BinDir(root, version string) (string, error) {
	dir := VersionDir(root, version)
	if _, err := os.Stat(dir); err != nil {
		return "", &NotInstalledError{Spec: version}
	}
	if runtime.GOOS != "windows" {
		dir = filepath.Join(dir, "bin")
	}
	return dir, nil
}

// DetectSpec walks up from dir looking for .nvmrc or .node-version and returns the version spec
// it names. Aliases like "lts/*" or "node" return "" so the caller falls back to its default.
func DetectSpec(dir string) string {
	spec, _ := DetectSpecFile(dir)
	return spec
}

// SpecFiles are the files a project names its Node.js version in, in the order they are looked for.
var SpecFiles = []string{".nvmrc", ".node-version"}

// DetectSpecFile is DetectSpec that also returns the file the spec came from.
func DetectSpecFile(dir string) (spec, file string) {
	dir = filepath.Clean(dir)
	for {
		for _, name := range SpecFiles {
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			spec := ValidSpec(string(data))
			if spec == "" {
				return "", ""
			}
			return spec, path
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

// ValidSpec returns a version spec ("22", "v22.11.0" → "22.11.0"), or "" for aliases and garbage.
func ValidSpec(s string) string {
	spec := strings.TrimPrefix(strings.TrimSpace(s), "v")
	if _, ok := parse(spec); !ok {
		return ""
	}
	return spec
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
