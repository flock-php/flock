package certs

import (
	"crypto/x509"
	"testing"
)

func TestLeavesIssueVerifiableCertificatesForDeepNamesAndReuseThem(t *testing.T) {
	ca, err := Ensure(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	leaves := NewLeaves()

	for _, name := range []string{"a.b.app.test", "x.y.z.w.app.test", "alias.test"} {
		c, err := leaves.Get(ca, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: "other." + name, Roots: pool}); err == nil {
			t.Errorf("%s: the certificate must cover exactly its name", name)
		}
		again, _ := leaves.Get(ca, name)
		if again != c {
			t.Errorf("%s: want the cached certificate", name)
		}
	}
}

func TestLeavesForNamesOutsideTheTLDFailVerification(t *testing.T) {
	ca, err := Ensure(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)

	c, err := NewLeaves().Get(ca, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: "example.com", Roots: pool}); err == nil {
		t.Fatal("the name-constrained CA must not produce a valid certificate for example.com")
	}
}
