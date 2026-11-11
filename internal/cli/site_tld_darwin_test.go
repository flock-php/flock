package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/certs"
	"flock/internal/config"
)

// TestUntrustRootArgs pins the macOS untrust command construction: the trust decisions go
// first (admin domain, then the user's), both best-effort — a certificate nothing ever
// trusted has nothing to remove — and the certificate itself leaves the login keychain by
// SHA-1 fingerprint, the way untrustCA has always deleted the global root.
func TestUntrustRootArgs(t *testing.T) {
	got := untrustRootArgs("/root/certs/roots/loc/ca.crt", "AABBCCDDEEFF", "/Users/u/Library/Keychains/login.keychain-db")
	want := [][]string{
		{"security", "remove-trusted-cert", "-d", "/root/certs/roots/loc/ca.crt"},
		{"security", "remove-trusted-cert", "/root/certs/roots/loc/ca.crt"},
		{"security", "delete-certificate", "-Z", "AABBCCDDEEFF", "/Users/u/Library/Keychains/login.keychain-db"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("command %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

// TestProvisionRootsOSConvergesTheSet pins the whole-set roots pass the macOS install
// checks and the provisioning sync share (F2 — install_darwin.go's ensurePrivileged runs
// this same function, so DNS, roots, trust and the bundle all converge from
// `flock install`): the sweep hands the stale store's fingerprint to the untrust hook
// (whose argv layer TestUntrustRootArgs pins) and deletes the store, every active root is
// created if missing and reaches the trust hook, the global root never does
// (ensurePrivileged's verify-cert covers it), and a declined dialog leaves the store
// intact for the next pass. The real keychain is never touched: the hooks are stubbed.
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

	// The sweep: the stale store's fingerprint reaches the untrust hook, and the store goes.
	wantUntrusted := []string{"stale|" + staleCert + "|" + staleFP}
	if !slices.Equal(untrusted, wantUntrusted) {
		t.Errorf("untrust hook: got %v, want %v", untrusted, wantUntrusted)
	}
	if fileExists(staleDir) {
		t.Error("the swept store must be deleted")
	}
	if !strings.Contains(out.String(), "✓ the .stale root certificate is no longer trusted") {
		t.Errorf("the sweep must report the retirement:\n%s", out.String())
	}

	// Create-if-missing and the whole-set trust: exactly the active non-global roots.
	locCert := filepath.Join(root, "certs", "roots", "loc", "ca.crt")
	if !fileExists(locCert) {
		t.Error("the .loc root must be created by the pass")
	}
	wantTrusted := []string{locCert}
	if !slices.Equal(trusted, wantTrusted) {
		t.Errorf("trust hook: got %v, want %v", trusted, wantTrusted)
	}
	if global := filepath.Join(root, "certs", "ca.crt"); slices.Contains(trusted, global) {
		t.Error("the global root must not reach the per-suffix trust loop")
	}

	// Converged: with the fingerprints reported trusted, the pass costs nothing.
	trusted = nil
	rootsTrusted = func(string) bool { return true }
	if err := provisionRootsOS(s, &out); err != nil {
		t.Fatalf("converged pass: %v", err)
	}
	if len(trusted) != 0 {
		t.Errorf("a converged set must not ask for trust again: %v", trusted)
	}

	// A declined untrust (errDeclined): the store stays, the error surfaces — the next
	// `flock install` retries the sweep.
	declinedDir := filepath.Join(root, "certs", "roots", "old2")
	if _, err := certs.Ensure(declinedDir, "old2"); err != nil {
		t.Fatal(err)
	}
	untrustRootHook = func(io.Writer, string, string, string) error { return errDeclined }
	if err := provisionRootsOS(s, &out); !errors.Is(err, errDeclined) {
		t.Fatalf("a declined dialog must surface as errDeclined, got %v", err)
	}
	if !fileExists(declinedDir) {
		t.Error("a declined untrust must leave the store for the next pass")
	}
}
