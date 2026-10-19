//go:build !windows && !darwin

package certs

import "os"

// bundleFiles are where distributions keep the trust store as one PEM file (the list Go's
// crypto/x509 reads), tried in order. SSL_CERT_FILE, when set, comes first.
var bundleFiles = []string{
	"/etc/ssl/certs/ca-certificates.crt",                // Debian, Ubuntu, Arch, Gentoo
	"/etc/pki/tls/certs/ca-bundle.crt",                  // Fedora, RHEL
	"/etc/ssl/ca-bundle.pem",                            // openSUSE
	"/etc/pki/tls/cacert.pem",                           // OpenELEC
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // CentOS, RHEL 7
	"/etc/ssl/cert.pem",                                 // Alpine, the BSDs
}

// SystemRoots returns the OS trust store as PEM, for tools (PHP) that cannot read it themselves.
func SystemRoots() []byte {
	files := bundleFiles
	if env := os.Getenv("SSL_CERT_FILE"); env != "" {
		files = append([]string{env}, files...)
	}
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}
