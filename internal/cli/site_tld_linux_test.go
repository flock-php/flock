//go:build linux

package cli

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/certs"
	"flock/internal/config"
)

// TestProvisionRootsOSConvergesTheSet pins the whole-set roots pass the Linux install
// checks and the provisioning sync share (F2 — install_linux.go's ensurePrivileged runs
// this same function): the sweep hands the stale store's certificate to the browsers'
// untrust hook (the certutil argv layer is pinned in the certs package) and deletes the
// store, every active root is created if missing and reaches the browser trust hook, and
// the global root never does (ensurePrivileged's anchors and Firefox policy cover it).
// The real NSS databases are never touched: the hooks are stubbed.
func TestProvisionRootsOSConvergesTheSet(t *testing.T) {
	root := t.TempDir()
	siteDir := filepath.Join(root, "sites", "demo")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := withDefaults()
	cfg.TLD = "test"
	if err := cfg.Upsert(config.Site{Name: "demo", Path: siteDir, TLD: "loc"}); err != nil {
		t.Fatal(err)
	}
	s := &state{root: root, cfg: cfg}

	staleDir := filepath.Join(root, "certs", "roots", "stale")
	if _, err := certs.Ensure(staleDir, "stale"); err != nil {
		t.Fatal(err)
	}
	staleCert := filepath.Join(staleDir, "ca.crt")
	staleFP, err := certs.FileFingerprint(staleCert)
	if err != nil {
		t.Fatal(err)
	}

	var untrusted, trusted []string
	oldUntrust, oldTrust, oldTrusted := untrustRootHook, trustRootHook, rootsTrusted
	t.Cleanup(func() {
		untrustRootHook, trustRootHook, rootsTrusted = oldUntrust, oldTrust, oldTrusted
	})
	untrustRootHook = func(_ io.Writer, suffix, certPath, fingerprint string) error {
		untrusted = append(untrusted, suffix+"|"+certPath+"|"+fingerprint)
		return nil
	}
	trustRootHook = func(certPath string) error {
		trusted = append(trusted, certPath)
		return nil
	}
	rootsTrusted = func(string) bool { return false }

	var out strings.Builder
	if err := provisionRootsOS(s, &out); err != nil {
		t.Fatalf("provisionRootsOS: %v\n%s", err, out.String())
	}

	wantUntrusted := []string{"stale|" + staleCert + "|" + staleFP}
	if !slices.Equal(untrusted, wantUntrusted) {
		t.Errorf("browser untrust hook: got %v, want %v", untrusted, wantUntrusted)
	}
	if fileExists(staleDir) {
		t.Error("the swept store must be deleted")
	}

	locCert := filepath.Join(root, "certs", "roots", "loc", "ca.crt")
	if !fileExists(locCert) {
		t.Error("the .loc root must be created by the pass")
	}
	wantTrusted := []string{locCert}
	if !slices.Equal(trusted, wantTrusted) {
		t.Errorf("browser trust hook: got %v, want %v", trusted, wantTrusted)
	}
	if global := filepath.Join(root, "certs", "ca.crt"); slices.Contains(trusted, global) {
		t.Error("the global root must not reach the per-suffix trust loop")
	}

	trusted = nil
	rootsTrusted = func(string) bool { return true }
	if err := provisionRootsOS(s, &out); err != nil {
		t.Fatalf("converged pass: %v", err)
	}
	if len(trusted) != 0 {
		t.Errorf("a converged set must not ask for trust again: %v", trusted)
	}
}
