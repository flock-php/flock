//go:build windows

package certs

import (
	"encoding/pem"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SystemRoots returns the Windows "Root" store (machine, user and third-party roots) as PEM,
// for tools (PHP's OpenSSL and curl) that cannot read it themselves.
func SystemRoots() []byte {
	name, _ := windows.UTF16PtrFromString("ROOT")
	store, err := windows.CertOpenSystemStore(0, name)
	if err != nil {
		return nil
	}
	defer windows.CertCloseStore(store, 0)

	var out []byte
	var cert *windows.CertContext
	for {
		cert, err = windows.CertEnumCertificatesInStore(store, cert)
		if err != nil || cert == nil {
			break
		}
		der := unsafe.Slice(cert.EncodedCert, cert.Length)
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out
}
