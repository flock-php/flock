package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"sync"
	"time"
)

// maxLeaves bounds the in-memory certificates; past it the cache starts over.
const maxLeaves = 1024

// Leaves issues certificates for single hostnames during TLS handshakes and keeps them in memory.
// A wildcard certificate only covers one level (*.app.test), so deeper names (a.b.app.test),
// aliases and unknown names get one of these instead of a file from SiteCert.
type Leaves struct {
	mu    sync.Mutex
	certs map[string]*tls.Certificate // "<CA fingerprint> <name>"
}

// NewLeaves returns an empty cache.
func NewLeaves() *Leaves {
	return &Leaves{certs: map[string]*tls.Certificate{}}
}

// Get returns a certificate for name signed by ca, issuing it on first use or when it nears expiry.
func (l *Leaves) Get(ca *CA, name string) (*tls.Certificate, error) {
	key := ca.Fingerprint() + " " + name

	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.certs[key]; ok && time.Until(c.Leaf.NotAfter) > renewalWindow {
		return c, nil
	}

	c, err := ca.leaf(name)
	if err != nil {
		return nil, err
	}
	if len(l.certs) >= maxLeaves {
		clear(l.certs)
	}
	l.certs[key] = c
	return c, nil
}

// leaf issues a certificate for exactly name.
func (ca *CA) leaf(name string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: name, Organization: []string{"Flock local development"}},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}
