package certs

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// The CA goes into the shared NSS database once, created when missing, and comes out again.
func TestBrowserTrustRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("certutil"); err != nil {
		t.Skip("certutil isn't installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	ca, err := Ensure(filepath.Join(t.TempDir(), "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}

	if TrustedInBrowsers(ca.CertPath()) {
		t.Fatal("trusted before")
	}
	for range 2 {
		if err := TrustInBrowsers(ca.CertPath()); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(home, ".pki", "nssdb")
	if nicks := nssNicknames(db, ca.Fingerprint()); len(nicks) != 1 || nicks[0] != nssNickname {
		t.Fatalf("nicknames %v", nicks)
	}
	if !TrustedInBrowsers(ca.CertPath()) {
		t.Fatal("not trusted after")
	}
	if err := UntrustInBrowsers(ca.CertPath()); err != nil {
		t.Fatal(err)
	}
	if TrustedInBrowsers(ca.CertPath()) {
		t.Fatal("trusted after removal")
	}
}

// Firefox names a policy import after the CA's subject, too long for certutil's nickname column.
func TestNicknamesWithLongNames(t *testing.T) {
	if _, err := exec.LookPath("certutil"); err != nil {
		t.Skip("certutil isn't installed")
	}
	db := t.TempDir()
	ca, err := Ensure(filepath.Join(t.TempDir(), "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	long := "Flock Local CA (someone@a-rather-long-host-name-for-a-laptop) - Flock local development"
	if err := certutil("-N", "-d", "sql:"+db, "--empty-password"); err != nil {
		t.Fatal(err)
	}
	if err := certutil("-A", "-d", "sql:"+db, "-t", "CT,C,", "-n", long, "-i", ca.CertPath()); err != nil {
		t.Fatal(err)
	}
	if nicks := nssNicknames(db, ca.Fingerprint()); len(nicks) != 1 || nicks[0] != long {
		t.Fatalf("nicknames %q", nicks)
	}
}
