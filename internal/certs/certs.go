// Package certs is Flock's local certificate authority (mkcert-style).
//
// The root is name-constrained to the local TLD (and localhost), so even if its key
// leaked it could not be used to impersonate real websites.
package certs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"time"
)

// Leaf certificates are valid for 825 days, the maximum macOS accepts for TLS server certs.
const (
	caValidity    = 10 * 365 * 24 * time.Hour
	leafValidity  = 825 * 24 * time.Hour
	renewalWindow = 30 * 24 * time.Hour
)

// CA is the local root certificate and its key.
type CA struct {
	dir  string
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// Ensure loads the CA from dir, creating it on first use.
func Ensure(dir, tld string) (*CA, error) {
	ca := &CA{dir: dir}

	if err := ca.load(); err == nil {
		return ca, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := ca.create(tld); err != nil {
		return nil, err
	}

	return ca, nil
}

// CertPath is the PEM file of the root certificate (what gets trusted by the OS).
func (ca *CA) CertPath() string { return filepath.Join(ca.dir, "ca.crt") }

func (ca *CA) keyPath() string { return filepath.Join(ca.dir, "ca.key") }

// Fingerprint is the SHA-1 of the root, as shown by Keychain Access and certutil.
func (ca *CA) Fingerprint() string {
	return fmt.Sprintf("%X", sha1.Sum(ca.cert.Raw))
}

// FileFingerprint is the SHA-1 of the certificate in a PEM file, like Fingerprint.
func FileFingerprint(path string) (string, error) {
	cert, err := readCert(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%X", sha1.Sum(cert.Raw)), nil
}

func (ca *CA) load() error {
	cert, err := readCert(ca.CertPath())
	if err != nil {
		return err
	}

	keyPEM, err := os.ReadFile(ca.keyPath())
	if err != nil {
		return err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return fmt.Errorf("%s: no PEM data", ca.keyPath())
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("%s: %w", ca.keyPath(), err)
	}

	ca.cert, ca.key = cert, key
	return nil
}

func (ca *CA) create(tld string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	name := "Flock Local CA"
	if u, err := user.Current(); err == nil {
		host, _ := os.Hostname()
		name = fmt.Sprintf("Flock Local CA (%s@%s)", u.Username, host)
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:                randomSerial(),
		Subject:                     pkix.Name{CommonName: name, Organization: []string{"Flock local development"}},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(caValidity),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"." + tld, tld, "localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(ca.dir, 0o700); err != nil {
		return err
	}
	if err := writeKey(ca.keyPath(), key); err != nil {
		return err
	}
	if err := writePEM(ca.CertPath(), "CERTIFICATE", der, 0o644); err != nil {
		return err
	}

	ca.cert, ca.key = cert, key
	return nil
}

// SiteCert returns cert/key paths for domain (covering domain and *.domain),
// issuing a new one when missing, expiring soon, or signed by a different CA.
func (ca *CA) SiteCert(domain string) (certPath, keyPath string, err error) {
	certPath, keyPath = SitePaths(ca.dir, domain)
	dir := filepath.Dir(certPath)

	if ca.leafIsCurrent(certPath, domain) {
		if _, err := os.Stat(keyPath); err == nil {
			return certPath, keyPath, nil
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: domain, Organization: []string{"Flock local development"}},
		DNSNames:     []string{domain, "*." + domain},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return "", "", err
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	if err := writeKey(keyPath, key); err != nil {
		return "", "", err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}

	return certPath, keyPath, nil
}

// SitePaths is where SiteCert stores a site's certificate and key inside the CA dir.
func SitePaths(caDir, domain string) (certPath, keyPath string) {
	dir := filepath.Join(caDir, "sites")
	return filepath.Join(dir, domain+".crt"), filepath.Join(dir, domain+".key")
}

func (ca *CA) leafIsCurrent(path, domain string) bool {
	cert, err := readCert(path)
	if err != nil {
		return false
	}
	if time.Until(cert.NotAfter) < renewalWindow || !slices.Contains(cert.DNSNames, domain) {
		return false
	}
	return cert.CheckSignatureFrom(ca.cert) == nil
}

// WriteBundle writes system (PEM roots, see SystemRoots) followed by the Flock root to path,
// leaving the file alone when it is already current. PHP and Node use it so they trust both
// public sites and other local .test sites.
func (ca *CA) WriteBundle(path string, system []byte) error {
	root, err := os.ReadFile(ca.CertPath())
	if err != nil {
		return err
	}

	out := append([]byte{}, system...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, root...)

	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, out) {
		return nil
	}
	return os.WriteFile(path, out, 0o644)
}

func readCert(path string) (*x509.Certificate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s: no PEM data", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "EC PRIVATE KEY", der, 0o600)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	return n
}
