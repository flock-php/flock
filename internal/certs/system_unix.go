//go:build !windows

package certs

import "os"

// SystemRoots returns the OS trust store as PEM, for tools (PHP) that cannot read it themselves.
func SystemRoots() []byte {
	data, _ := os.ReadFile("/etc/ssl/cert.pem")
	return data
}
