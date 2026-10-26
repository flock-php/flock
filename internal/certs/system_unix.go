//go:build !windows && !darwin

package certs

import "os"

// bundleFiles are where distributions keep the system trust store as one PEM file, the list Go's
// crypto/x509 reads on Linux. update-ca-certificates and update-ca-trust rebuild them, so they
// include what an administrator added (a company's TLS-inspection CA, Flock's own).
var bundleFiles = []string{
	"/etc/ssl/certs/ca-certificates.crt",                // Debian, Ubuntu, Arch
	"/etc/pki/tls/certs/ca-bundle.crt",                  // Fedora, RHEL
	"/etc/ssl/ca-bundle.pem",                            // openSUSE
	"/etc/pki/tls/cacert.pem",                           // OpenELEC
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // CentOS, RHEL 7
	"/etc/ssl/cert.pem",                                 // Alpine, BSDs
}

// SystemRoots returns the OS trust store as PEM, for tools (PHP) that cannot read it themselves:
// SSL_CERT_FILE when set, as for Go and OpenSSL, otherwise the first bundle that exists.
func SystemRoots() []byte {
	files := bundleFiles
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		files = []string{f}
	}
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}
