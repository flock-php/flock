package certs

import (
	"crypto/x509"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func verify(t *testing.T, ca *CA, certPath, host string) error {
	t.Helper()

	leaf, err := readCert(certPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)

	_, err = leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: pool})
	return err
}

func TestIssuesSiteCertsValidForDomainAndSubdomains(t *testing.T) {
	ca, err := Ensure(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}

	certPath, _, err := ca.SiteCert("watchtower.test")
	if err != nil {
		t.Fatal(err)
	}

	for _, host := range []string{"watchtower.test", "api.watchtower.test"} {
		if err := verify(t, ca, certPath, host); err != nil {
			t.Fatalf("%s: %v", host, err)
		}
	}
	if err := verify(t, ca, certPath, "other.test"); err == nil {
		t.Fatal("cert must not be valid for other.test")
	}
}

func TestReusesCAAndCurrentCerts(t *testing.T) {
	dir := t.TempDir()
	first, _ := Ensure(dir, "test")
	certPath, _, _ := first.SiteCert("app.test")
	before, _ := os.ReadFile(certPath)

	second, err := Ensure(dir, "test")
	if err != nil || second.Fingerprint() != first.Fingerprint() {
		t.Fatalf("CA was not reused: %v", err)
	}

	_, _, _ = second.SiteCert("app.test")
	after, _ := os.ReadFile(certPath)
	if string(before) != string(after) {
		t.Fatal("current certificate was reissued")
	}
}

func TestEnsureRecreatesRootWhenTLDChanges(t *testing.T) {
	dir := t.TempDir()

	first, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}

	second, err := Ensure(dir, "loc")
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint() == first.Fingerprint() {
		t.Fatal("root constrained to the old TLD was kept")
	}
	if !slices.Contains(second.cert.PermittedDNSDomains, ".loc") {
		t.Fatalf("new root is not constrained to .loc: %v", second.cert.PermittedDNSDomains)
	}

	third, err := Ensure(dir, "loc")
	if err != nil {
		t.Fatal(err)
	}
	if third.Fingerprint() != second.Fingerprint() {
		t.Fatal("a matching root was recreated on every load")
	}
}

// A create interrupted between writing ca.key and ca.crt leaves the certificate of one root
// next to the key of another. Ensure must replace that pair, not keep signing with it.
func TestEnsureRecreatesRootWhenStoredKeyDoesNotPairWithCert(t *testing.T) {
	dir := t.TempDir()

	stored, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Ensure(t.TempDir(), "test") // same TLD, so pairing is the only reason to replace
	if err != nil {
		t.Fatal(err)
	}

	keyPEM, err := os.ReadFile(other.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stored.keyPath(), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	fixed, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Fingerprint() == stored.Fingerprint() {
		t.Fatal("root whose key stopped pairing with its cert was kept")
	}
	if !fixed.pairs() {
		t.Fatal("recreated root does not pair its key with its cert")
	}

	certPath, _, err := fixed.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, fixed, certPath, "app.test"); err != nil {
		t.Fatalf("app.test: %v", err)
	}
}

func TestRootIsNameConstrainedToTheTLD(t *testing.T) {
	ca, _ := Ensure(t.TempDir(), "test")

	// Issuing outside the constraint succeeds at signing time, but no verifier accepts it.
	certPath, _, err := ca.SiteCert("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, ca, certPath, "example.com"); err == nil {
		t.Fatal("name constraint not enforced")
	}
}

// tmpFiles lists every *.tmp file under dir. create writes the root through temp files that it
// renames into place, so a completed Ensure must leave none of them behind.
func tmpFiles(t *testing.T, dir string) []string {
	t.Helper()

	var found []string
	if err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".tmp") {
			found = append(found, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return found
}

// TestCreateLeavesNoPartialState pins how create publishes a root: both files are written to
// temp files and os.Renamed into place, the key first and the certificate last.
//
// Writing them in place is what breaks: a crash during the ca.crt write leaves a truncated PEM,
// and load() reports a parse error for that, not os.ErrNotExist, so Ensure returns the error
// forever and the daemon stays degraded until ca.crt is deleted by hand. A rename is atomic, so
// each file lands whole. That leaves exactly one reachable torn state — the window between the
// two renames, where ca.crt is still the certificate of the previous root while ca.key is the
// new root's key. It is the pairing mismatch Ensure already detects and repairs, asserted below
// by building it the way the pairing test does. The temp-file assertions cover the residue: a
// crash outside that window leaves at most an unreferenced *.tmp, never a half-written root.
func TestCreateLeavesNoPartialState(t *testing.T) {
	dir := t.TempDir()

	ca, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if tmp := tmpFiles(t, dir); len(tmp) > 0 {
		t.Fatalf("create left temp files behind: %v", tmp)
	}

	keyInfo, err := os.Stat(ca.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("ca.key mode = %o, want 0600", keyInfo.Mode().Perm())
	}
	certInfo, err := os.Stat(ca.CertPath())
	if err != nil {
		t.Fatal(err)
	}
	if certInfo.Mode().Perm() != 0o644 {
		t.Fatalf("ca.crt mode = %o, want 0644", certInfo.Mode().Perm())
	}

	// A root from elsewhere, same TLD, so the pairing is the only reason to replace.
	other, err := Ensure(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// Land the state the between-renames crash leaves: cert from CA-A, key from CA-B.
	keyPEM, err := os.ReadFile(other.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ca.keyPath(), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	fixed, err := Ensure(dir, "test")
	if err != nil {
		t.Fatalf("Ensure did not heal the between-renames state: %v", err)
	}
	if fixed.Fingerprint() == ca.Fingerprint() {
		t.Fatal("the mismatched root was kept")
	}
	if !fixed.pairs() || !fixed.serves("test") {
		t.Fatal("the recreated root is not a usable root")
	}
	if tmp := tmpFiles(t, dir); len(tmp) > 0 {
		t.Fatalf("the recreate left temp files behind: %v", tmp)
	}

	certPath, _, err := fixed.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, fixed, certPath, "app.test"); err != nil {
		t.Fatalf("app.test: %v", err)
	}
}
