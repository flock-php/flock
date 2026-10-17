package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallTreeCopiesFilesModesAndSymlinks(t *testing.T) {
	requireSymlinks(t)
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "lib", "libpq.5.dylib"), []byte("lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("libpq.5.dylib", filepath.Join(src, "lib", "libpq.dylib")); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "pkg")
	if err := os.MkdirAll(filepath.Join(dst, "stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallTree(src, dst); err != nil {
		t.Fatal(err)
	}

	if link, err := os.Readlink(filepath.Join(dst, "lib", "libpq.dylib")); err != nil || link != "libpq.5.dylib" {
		t.Fatalf("symlink not preserved: %q %v", link, err)
	}
	// Windows has no execute bit to preserve.
	if info, err := os.Stat(filepath.Join(dst, "lib", "libpq.5.dylib")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0) {
		t.Fatalf("mode not preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale")); !os.IsNotExist(err) {
		t.Fatal("previous install must be replaced")
	}
}

// requireSymlinks skips a test where symbolic links can't be made: Windows allows them only in
// Developer Mode or for administrators (GitHub's runners are).
func requireSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symbolic links aren't allowed here: %v", err)
	}
}
