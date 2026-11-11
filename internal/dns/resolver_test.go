package dns

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolverProblem(t *testing.T) {
	old := ResolverDir
	ResolverDir = t.TempDir()
	t.Cleanup(func() { ResolverDir = old })

	if p := ResolverProblem("test", 53535); !strings.Contains(p, "missing") || !strings.Contains(p, "flock install") {
		t.Fatalf("missing file: %q", p)
	}

	if err := os.WriteFile(filepath.Join(ResolverDir, "test"), []byte(ResolverContent(53535)), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ResolverProblem("test", 53535); p != "" {
		t.Fatalf("current file: %q", p)
	}
	if p := ResolverProblem("test", 5300); !strings.Contains(p, "port 5300") || !strings.Contains(p, "flock install") {
		t.Fatalf("other port: %q", p)
	}
	if got := ResolverContent(53535); got != "# Managed by Flock\nnameserver 127.0.0.1\nport 53535\n" {
		t.Fatalf("content changed (must match what flock install writes): %q", got)
	}
}

// SyncResolverFiles converges a directory onto the suffix set: write every missing file, remove
// only Flock-marked files whose suffix left the set, and never touch a foreign file.
func TestSyncResolverFilesSetDiff(t *testing.T) {
	dir := t.TempDir()

	// "test" is current, "dev" is Flock's for a suffix that left the set, "corp" belongs to
	// someone else.
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte(ResolverContent(53535)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "dev"), []byte(ResolverContent(53535)), 0o644)
	foreign := "nameserver 1.1.1.1\nport 53\n"
	_ = os.WriteFile(filepath.Join(dir, "corp"), []byte(foreign), 0o644)

	written, removed, err := SyncResolverFiles(dir, []string{"test", "loc"}, 53535)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "loc")}; !slices.Equal(written, want) {
		t.Errorf("written = %v, want %v", written, want)
	}
	if want := []string{filepath.Join(dir, "dev")}; !slices.Equal(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "loc")); err != nil || string(data) != ResolverContent(53535) {
		t.Errorf("loc: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "corp")); err != nil || string(data) != foreign {
		t.Errorf("the foreign file was touched: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test")); err != nil {
		t.Errorf("the current file went away: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dev")); !os.IsNotExist(err) {
		t.Errorf("the stale Flock file survived: %v", err)
	}

	// A second sync over the covered set writes and removes nothing.
	written, removed, err = SyncResolverFiles(dir, []string{"test", "loc"}, 53535)
	if err != nil || len(written) != 0 || len(removed) != 0 {
		t.Fatalf("second sync: written %v, removed %v, err %v", written, removed, err)
	}
}

// ResolversCurrent answers before the password prompt: only Flock-marked files, on the right
// port, over exactly the set, count as current.
func TestResolversCurrent(t *testing.T) {
	dir := t.TempDir()
	if ResolversCurrent(dir, []string{"test"}, 53535) {
		t.Fatal("an empty dir is current")
	}
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte(ResolverContent(53535)), 0o644)
	if !ResolversCurrent(dir, []string{"test"}, 53535) {
		t.Fatal("the covered set isn't current")
	}
	if ResolversCurrent(dir, []string{"test", "loc"}, 53535) {
		t.Fatal("a missing suffix counts as current")
	}
	if ResolversCurrent(dir, []string{"test"}, 5300) {
		t.Fatal("a file for the wrong port counts as current")
	}
	if _, _, err := SyncResolverFiles(dir, []string{"test", "loc"}, 53535); err != nil {
		t.Fatal(err)
	}
	if !ResolversCurrent(dir, []string{"test", "loc"}, 53535) {
		t.Fatal("not current after the sync")
	}
	_ = os.WriteFile(filepath.Join(dir, "old"), []byte(ResolverContent(53535)), 0o644)
	if ResolversCurrent(dir, []string{"test", "loc"}, 53535) {
		t.Fatal("a stale Flock-marked file outside the set counts as current")
	}
	if _, _, err := SyncResolverFiles(dir, []string{"test", "loc"}, 53535); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "corp"), []byte(foreignResolver), 0o644)
	if !ResolversCurrent(dir, []string{"test", "loc"}, 53535) {
		t.Fatal("a foreign file (no Flock marker) must not need the password")
	}
}

const foreignResolver = "nameserver 1.1.1.1\nport 53\n"

// RemoveResolvers deletes only the Flock-marked files of the listed suffixes.
func TestRemoveResolverFilesKeepsForeign(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "test"), []byte(ResolverContent(53535)), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "corp"), []byte(foreignResolver), 0o644)

	removed := RemoveResolverFiles(dir, []string{"test", "corp", "dev"})
	if want := []string{filepath.Join(dir, "test")}; !slices.Equal(removed, want) {
		t.Fatalf("removed %v, want %v", removed, want)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "corp")); err != nil {
		t.Fatalf("the foreign file went away: %v", err)
	}
}

// StaleResolvers lists the Flock-marked resolver files in dir whose suffix is outside the
// active set. Foreign files (no marker), non-label names and directories are ignored.
func TestStaleResolversIgnoresForeignAndActive(t *testing.T) {
	dir := t.TempDir()
	for _, s := range []string{"test", "old", "xyz", "a_b"} {
		if err := os.WriteFile(filepath.Join(dir, s), []byte(ResolverContent(53535)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "corp"), []byte(foreignResolver), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := StaleResolvers(dir, []string{"test"}); !slices.Equal(got, []string{"old", "xyz"}) {
		t.Fatalf("got %v, want [old xyz]", got)
	}
	if got := StaleResolvers(dir, nil); !slices.Equal(got, []string{"old", "test", "xyz"}) {
		t.Fatalf("an empty active set: %v", got)
	}
	if got := StaleResolvers(filepath.Join(dir, "nope"), []string{"test"}); got != nil {
		t.Fatalf("a missing dir is nothing's problem: %v", got)
	}
}
