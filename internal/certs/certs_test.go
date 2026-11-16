package certs

import (
	"crypto/x509"
	"os"
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

func TestReplacesCAWhenTheTLDChanges(t *testing.T) {
	dir := t.TempDir()
	old, _ := Ensure(dir, "wsl")
	if _, _, err := old.SiteCert("app.wsl"); err != nil {
		t.Fatal(err)
	}

	ca, err := Ensure(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if ca.Fingerprint() == old.Fingerprint() {
		t.Fatal("CA for .wsl was kept for .test")
	}
	certPath, _, err := ca.SiteCert("app.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, ca, certPath, "app.test"); err != nil {
		t.Fatalf("app.test: %v", err)
	}

	again, err := Ensure(dir, "test")
	if err != nil || again.Fingerprint() != ca.Fingerprint() {
		t.Fatalf("new CA was not reused: %v", err)
	}
}

func TestKeepsCAWithoutNameConstraints(t *testing.T) {
	ca := &CA{cert: &x509.Certificate{}}
	if !ca.permits("test") {
		t.Fatal("a CA without name constraints allows every TLD")
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
