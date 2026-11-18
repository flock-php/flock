package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestRegeneratesMismatchedCA(t *testing.T) {
	dir := t.TempDir()
	first, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeKey(first.keyPath(), key); err != nil {
		t.Fatal(err)
	}
	if err := (&CA{dir: dir}).load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load mismatch: got %v, want os.ErrNotExist", err)
	}

	second, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint() == first.Fingerprint() {
		t.Fatal("mismatched CA was reused")
	}
	certPath, _, err := second.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, second, certPath, "app.test"); err != nil {
		t.Fatal(err)
	}
	if err := (&CA{dir: dir}).load(); err != nil {
		t.Fatalf("load regenerated CA: %v", err)
	}
}

func TestEnsurePreservesCertificateReadErrors(t *testing.T) {
	dir := t.TempDir()
	ca, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ca.CertPath(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ca.CertPath(), 0o644) })
	if _, err := os.ReadFile(ca.CertPath()); err == nil {
		t.Skip("certificate permissions are not enforced")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if _, err := Ensure(dir, "test"); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("Ensure: got %v, want permission error", err)
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

func TestNewCAWhenTheTLDChanges(t *testing.T) {
	dir := t.TempDir()
	first, _ := Ensure(dir, "wsl")

	second, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint() == first.Fingerprint() {
		t.Fatal("CA for .wsl was kept for .test")
	}

	certPath, _, err := second.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, second, certPath, "app.test"); err != nil {
		t.Fatal(err)
	}
}

func TestCARotationKeepsPEMPermissions(t *testing.T) {
	dir := t.TempDir()
	for _, tld := range []string{"wsl", "test"} {
		ca, err := Ensure(dir, tld)
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range []struct {
			path string
			mode os.FileMode
		}{{ca.keyPath(), 0o600}, {ca.CertPath(), 0o644}} {
			info, err := os.Stat(file.path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != file.mode {
				t.Fatalf("%s: mode %o, want %o", file.path, info.Mode().Perm(), file.mode)
			}
		}
		checkCAFiles(t, dir)
	}
}

func TestPEMRenameFailureCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ca.crt")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(path, "keep")
	if err := os.WriteFile(keep, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePEM(path, "CERTIFICATE", []byte("replacement"), 0o644); err == nil {
		t.Fatal("replacing a directory succeeded")
	}
	if got := string(readCAFile(t, keep)); got != "unchanged" {
		t.Fatalf("existing target changed: %q", got)
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("temporary file left after rename failure: %v", files)
	}
}

func TestEnsurePreservesMalformedCAErrors(t *testing.T) {
	for _, name := range []string{"ca.key", "ca.crt"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Ensure(dir, "wsl"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("not PEM"), 0o600); err != nil {
				t.Fatal(err)
			}
			key := string(readCAFile(t, filepath.Join(dir, "ca.key")))
			cert := string(readCAFile(t, filepath.Join(dir, "ca.crt")))
			if _, err := Ensure(dir, "test"); err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("malformed CA: got %v, want a decode error", err)
			}
			if string(readCAFile(t, filepath.Join(dir, "ca.key"))) != key || string(readCAFile(t, filepath.Join(dir, "ca.crt"))) != cert {
				t.Fatal("malformed CA was overwritten")
			}
		})
	}
}

func readCAFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func checkCAFiles(t *testing.T, dir string) {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name() != "ca.crt" || files[1].Name() != "ca.key" {
		t.Fatalf("unexpected CA directory contents: %v", files)
	}
}

func TestSiteCertReusesMatchingTLSKeyPair(t *testing.T) {
	ca, certPath, keyPath := issueTestSiteCert(t)
	cert := string(readCAFile(t, certPath))
	key := string(readCAFile(t, keyPath))
	againCert, againKey, err := ca.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if againCert != certPath || againKey != keyPath {
		t.Fatal("current site certificate paths changed")
	}
	if string(readCAFile(t, certPath)) != cert || string(readCAFile(t, keyPath)) != key {
		t.Fatal("matching current TLS keypair was reissued")
	}
	checkSiteTLSCertificate(t, ca, certPath, keyPath)
}

func issueTestSiteCert(t *testing.T) (*CA, string, string) {
	t.Helper()
	ca, err := Ensure(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath, err := ca.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	checkSiteTLSCertificate(t, ca, certPath, keyPath)
	return ca, certPath, keyPath
}

func checkSiteTLSCertificate(t *testing.T, ca *CA, certPath, keyPath string) {
	t.Helper()
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	if err := verify(t, ca, certPath, "app.test"); err != nil {
		t.Fatal(err)
	}
}
