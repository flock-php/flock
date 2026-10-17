package postgres

import (
	"regexp"
	"sort"
	"strconv"

	"flock/internal/binaries"
)

// DefaultMajor is the major `flock postgres install` installs without arguments.
const DefaultMajor = "18"

// EndOfLife is when each major stops receiving fixes upstream (postgresql.org/support/versioning).
var EndOfLife = map[string]string{
	"14": "2026-11-12",
	"15": "2027-11-11",
	"16": "2028-11-09",
	"17": "2029-11-08",
	"18": "2030-11-14",
}

var (
	majorPattern   = regexp.MustCompile(`^\d+$`)
	versionPattern = regexp.MustCompile(`^(\d+)\.\d+(?:\.\d+)?$`)
)

// MajorOf returns the major of a version or install ID ("18.6", "18.6-build1" → "18"), or "".
func MajorOf(version string) string {
	if v := VersionOf(version); v != "" {
		version = v
	}
	if m := versionPattern.FindStringSubmatch(version); m != nil {
		return m[1]
	}
	return ""
}

// Newer reports whether install ID a is newer than b: a later version, or a higher build of it.
func Newer(a, b string) bool {
	va, ba, _ := ParseID(a)
	vb, bb, _ := ParseID(b)
	if c := binaries.CompareVersions(va, vb); c != 0 {
		return c > 0
	}
	return ba > bb
}

// offers is the newest build of every major the manifest has for platform.
func offers(manifest *binaries.Manifest, platform string) map[string]binaries.Version {
	out := map[string]binaries.Version{}
	for _, v := range manifest.Versions(Package, platform) { // newest first
		if major := MajorOf(v.Version); major != "" {
			if _, ok := out[major]; !ok {
				out[major] = v
			}
		}
	}
	return out
}

// sortMajors sorts majors newest first.
func sortMajors(majors []string) {
	sort.Slice(majors, func(i, j int) bool {
		a, _ := strconv.Atoi(majors[i])
		b, _ := strconv.Atoi(majors[j])
		return a > b
	})
}
