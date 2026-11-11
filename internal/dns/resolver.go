package dns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ResolverDir is where macOS reads per-domain resolver files (resolver(5)). Tests move it.
var ResolverDir = "/etc/resolver"

// ResolverMarker marks the resolver files Flock owns: only files that carry it get removed,
// files without it belong to other programs.
const ResolverMarker = "# Managed by Flock"

// ResolverFile is the resolver file `flock install` writes on macOS to send tld to Flock.
func ResolverFile(tld string) string { return filepath.Join(ResolverDir, tld) }

// ResolverContent is what ResolverFile holds when it sends lookups to Flock's DNS server on port.
func ResolverContent(port int) string {
	return fmt.Sprintf("%s\nnameserver 127.0.0.1\nport %d\n", ResolverMarker, port)
}

// SyncResolvers converges ResolverDir onto the suffix set (see SyncResolverFiles).
func SyncResolvers(tlds []string, port int) (written, removed []string, err error) {
	return SyncResolverFiles(ResolverDir, tlds, port)
}

// SyncResolverFiles makes the Flock-owned resolver files in dir cover exactly tlds, each sending
// lookups to Flock's DNS server on port: every missing (or outdated) file is written, and only
// Flock-marked files whose suffix left the set are removed. Files without the marker belong to
// other programs and are left alone (install replaces a foreign file of a wanted suffix, as the
// old single-TLD setup did). Tests pass a temp dir; install code passes "/etc/resolver".
func SyncResolverFiles(dir string, tlds []string, port int) (written, removed []string, err error) {
	_, missing, stale := resolverDiff(dir, tlds, port)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", dir, err)
	}
	content := ResolverContent(port)
	for _, path := range missing {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return written, removed, fmt.Errorf("write %s: %w", path, err)
		}
		written = append(written, path)
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil {
			return written, removed, fmt.Errorf("remove %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	return written, removed, nil
}

// ResolversCurrent reports whether dir's Flock-marked resolver files cover exactly tlds on port.
// Install checks it, so a converged set costs no password prompt; a foreign file, marked or not,
// that isn't one of the wanted suffixes is none of Flock's business.
func ResolversCurrent(dir string, tlds []string, port int) bool {
	_, missing, stale := resolverDiff(dir, tlds, port)
	return len(missing) == 0 && len(stale) == 0
}

// RemoveResolvers deletes ResolverDir's Flock-marked files for tlds (uninstall, retire).
func RemoveResolvers(tlds []string) []string { return RemoveResolverFiles(ResolverDir, tlds) }

// RemoveResolverFiles deletes, in dir, the files for tlds that carry Flock's marker.
func RemoveResolverFiles(dir string, tlds []string) []string {
	var removed []string
	for _, tld := range normalizeSuffixes(tlds) {
		path := filepath.Join(dir, tld)
		data, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(data), ResolverMarker) {
			continue
		}
		if err := os.Remove(path); err == nil {
			removed = append(removed, path)
		}
	}
	return removed
}

// resolverDiff lists what dir is missing for the set and which of its Flock-marked files are
// stale; foreign files never appear.
func resolverDiff(dir string, tlds []string, port int) (want map[string]bool, missing, stale []string) {
	want = map[string]bool{}
	content := ResolverContent(port)
	for _, tld := range normalizeSuffixes(tlds) {
		want[tld] = true
		path := filepath.Join(dir, tld)
		if data, err := os.ReadFile(path); err != nil || string(data) != content {
			missing = append(missing, path)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return want, missing, nil
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || want[name] {
			continue
		}
		path := filepath.Join(dir, name)
		if data, err := os.ReadFile(path); err == nil && strings.HasPrefix(string(data), ResolverMarker) {
			stale = append(stale, path)
		}
	}
	slices.Sort(missing)
	slices.Sort(stale)
	return want, missing, stale
}

// ResolverProblem explains why the resolver file for tld does not send lookups to Flock's DNS
// server on port, or returns "" when it does.
func ResolverProblem(tld string, port int) string {
	path := ResolverFile(tld)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("%s is missing, so macOS doesn't send .%s lookups to Flock — run `flock install`", path, tld)
	case err != nil:
		return fmt.Sprintf("can't read %s (%v) — run `flock install`", path, err)
	case string(data) != ResolverContent(port):
		return fmt.Sprintf("%s doesn't point at Flock's DNS server on port %d — run `flock install`", path, port)
	}
	return ""
}
