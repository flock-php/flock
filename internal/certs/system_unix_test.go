//go:build !windows && !darwin

package certs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemRootsReadsTheFirstBundleThatExists(t *testing.T) {
	dir := t.TempDir()
	missing, empty, debian, alpine := filepath.Join(dir, "missing"), filepath.Join(dir, "empty"), filepath.Join(dir, "debian"), filepath.Join(dir, "alpine")
	for path, data := range map[string]string{empty: "", debian: "debian roots", alpine: "alpine roots"} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := bundleFiles
	bundleFiles = []string{missing, empty, debian, alpine}
	t.Cleanup(func() { bundleFiles = old })
	t.Setenv("SSL_CERT_FILE", "")

	if got := string(SystemRoots()); got != "debian roots" {
		t.Errorf("SystemRoots() = %q, want the Debian bundle", got)
	}

	t.Setenv("SSL_CERT_FILE", alpine)
	if got := string(SystemRoots()); got != "alpine roots" {
		t.Errorf("with SSL_CERT_FILE: %q, want its bundle", got)
	}

	bundleFiles = []string{missing}
	t.Setenv("SSL_CERT_FILE", "")
	if got := SystemRoots(); got != nil {
		t.Errorf("without a bundle: %q, want nil", got)
	}
}
