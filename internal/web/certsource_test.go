package web

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"path/filepath"
	"testing"

	"flock/internal/certs"
)

// rootsSource wires a certSource the way the daemon does: the names HTTPS answers for are the
// suffixes Flock has a root for, and a name's certificate comes from that suffix's own root.
func rootsSource(t *testing.T, dir, global string, suffixes []string) (*certSource, *certs.Roots) {
	t.Helper()

	roots := certs.OpenRoots(dir, global)
	if err := roots.Ensure(suffixes); err != nil {
		t.Fatal(err)
	}
	leaves := certs.NewLeaves()

	s, err := newCertSource(Config{
		TLD:   global,
		Roots: roots,
		Issue: func(name string) (*tls.Certificate, error) {
			ca, ok := roots.For(name)
			if !ok {
				return nil, fmt.Errorf("no certificate authority for %s", name)
			}
			return leaves.Get(ca, name)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return s, roots
}

// TestALocHostIsServedOnlyWhenTheLocRootExists pins the per-suffix handshake: a name gets a
// leaf from its own suffix's root, and a suffix Flock has no root for gets the fail-fast
// "no certificate" answer instead of a leaf some other root signs and every verifier rejects.
func TestALocHostIsServedOnlyWhenTheLocRootExists(t *testing.T) {
	s, roots := rootsSource(t, t.TempDir(), "test", []string{"loc", "test"})

	for _, name := range []string{"demo.loc", "x.demo.loc", "app.test", "a.b.app.test"} {
		c, err := s.certificate(&tls.ClientHelloInfo{ServerName: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ca, ok := roots.For(name)
		if !ok {
			t.Fatalf("no root for %s", name)
		}
		pool := x509.NewCertPool()
		pool.AddCert(ca.Cert())
		if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool}); err != nil {
			t.Errorf("%s: the leaf does not verify against its own suffix's root: %v", name, err)
		}
	}

	for _, name := range []string{"demo.dev", "demo.xloc", "block", "example.com"} {
		if c, err := s.certificate(&tls.ClientHelloInfo{ServerName: name}); err == nil {
			t.Errorf("%s: want no certificate, got one for %v", name, c.Leaf.DNSNames)
		}
	}

	// The same name with no root for its suffix: nothing is served for it.
	withoutLoc, _ := rootsSource(t, t.TempDir(), "test", []string{"test"})
	if _, err := withoutLoc.certificate(&tls.ClientHelloInfo{ServerName: "demo.loc"}); err == nil {
		t.Error("demo.loc was served without a root for .loc")
	}
	if _, err := withoutLoc.certificate(&tls.ClientHelloInfo{ServerName: "demo.test"}); err != nil {
		t.Errorf("demo.test: %v", err)
	}
}

// TestTheGlobalTLDAloneIsServed keeps the single-suffix config working: a Config that names no
// roots answers its one TLD, the way it did before per-suffix roots.
func TestTheGlobalTLDAloneIsServed(t *testing.T) {
	dir := t.TempDir()
	ca, err := certs.Ensure(filepath.Join(dir, "certs"), "test")
	if err != nil {
		t.Fatal(err)
	}
	leaves := certs.NewLeaves()

	s, err := newCertSource(Config{TLD: "test", Issue: func(name string) (*tls.Certificate, error) {
		return leaves.Get(ca, name)
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.test", "a.b.app.test"} {
		if _, err := s.certificate(&tls.ClientHelloInfo{ServerName: name}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"app.loc", "xloc"} {
		if _, err := s.certificate(&tls.ClientHelloInfo{ServerName: name}); err == nil {
			t.Errorf("%s: want no certificate under a config with only .test", name)
		}
	}
}
