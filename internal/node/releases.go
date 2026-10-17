package node

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"flock/internal/binaries"
)

// MinimumMajor is the oldest line offered for installing: older LTS lines stay listed because
// Laravel projects pin them, like PHP 7.4.
const MinimumMajor = 16

// Line is one major version of Node.js, e.g. 22.
type Line struct {
	Major  string `json:"major"`
	Latest string `json:"latest"` // newest release in the channel, e.g. "22.11.0"
}

// Lines returns the majors the manifest publishes for this platform, newest first.
func Lines(manifest *binaries.Manifest) []Line {
	var lines []Line
	for major, v := range published(manifest) {
		if majorOf(major) >= MinimumMajor {
			lines = append(lines, Line{Major: major, Latest: v.Version})
		}
	}
	sortLines(lines)
	return lines
}

// Newer reports whether version a is newer than b ("22.11.0" > "22.9.1").
func Newer(a, b string) bool {
	versions := []string{a, b}
	sortDesc(versions)
	return versions[0] == a && a != b
}

func sortLines(lines []Line) {
	slices.SortFunc(lines, func(a, b Line) int { return cmp.Compare(majorOf(b.Major), majorOf(a.Major)) })
}

func majorOf(v string) int {
	parts, _ := parse(v)
	if len(parts) == 0 {
		return -1
	}
	return parts[0]
}

// MajorOf returns the major of a version ("22.11.0" â†’ "22"), or "" when it isn't one.
func MajorOf(version string) string {
	if n := majorOf(version); n >= 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// Uninstall removes every installed version of major, and the global packages installed with it.
func Uninstall(root, major string) error {
	if err := os.RemoveAll(GlobalDir(root, major)); err != nil {
		return err
	}
	versions, err := Installed(root)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if MajorOf(v) == major {
			if err := os.RemoveAll(filepath.Join(Dir(root), v)); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveOlder deletes the installed versions of version's major older than it (after an update).
// A version still in use (a running dev server) stays; Resolve picks the newest anyway.
func RemoveOlder(root, version string) {
	versions, _ := Installed(root)
	for _, v := range versions {
		if v != version && MajorOf(v) == MajorOf(version) && Newer(version, v) {
			_ = os.RemoveAll(filepath.Join(Dir(root), v))
		}
	}
}

// Catalog caches npm's registry data (which npm each Node.js version can update to), so the
// settings pane can ask often.
type Catalog struct {
	Installer *Installer
	MaxAge    time.Duration

	mu           sync.Mutex
	npm          *npmPackument
	npmCheckedAt time.Time
	npmErr       error
}
