//go:build darwin

package certs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"
)

func testCert(t *testing.T, name string) (pemData []byte, sha string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), strings.ToUpper(hex.EncodeToString(sum[:]))
}

// trustPlist is an exported trust settings plist; entries maps a SHA-1 to its trustSettings
// array XML ("" leaves the key out, which means "always trust").
func trustPlist(entries map[string]string) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>trustList</key><dict>`)
	for hash, settings := range entries {
		fmt.Fprintf(&b, "<key>%s</key><dict><key>serialNumber</key><data>AQ==</data>", hash)
		if settings != "" {
			fmt.Fprintf(&b, "<key>trustSettings</key>%s", settings)
		}
		b.WriteString("</dict>")
	}
	b.WriteString(`</dict><key>trustVersion</key><integer>1</integer></dict></plist>`)
	return []byte(b.String())
}

const (
	sslRoot  = `<array><dict><key>kSecTrustSettingsPolicy</key><data>KoZIhvdjZAED</data><key>kSecTrustSettingsPolicyName</key><string>sslServer</string><key>kSecTrustSettingsResult</key><integer>1</integer></dict></array>`
	sslDeny  = `<array><dict><key>kSecTrustSettingsPolicy</key><data>KoZIhvdjZAED</data><key>kSecTrustSettingsResult</key><integer>3</integer></dict></array>`
	eapOnly  = `<array><dict><key>kSecTrustSettingsPolicy</key><data>KoZIhvdjZAEP</data><key>kSecTrustSettingsPolicyName</key><string>eapServer</string><key>kSecTrustSettingsResult</key><integer>2</integer></dict></array>`
	oneHost  = `<array><dict><key>kSecTrustSettingsPolicy</key><data>KoZIhvdjZAED</data><key>kSecTrustSettingsPolicyString</key><string>example.com</string><key>kSecTrustSettingsResult</key><integer>1</integer></dict></array>`
	emptyArr = `<array/>`
)

func TestMergeRootsAddsExplicitlyTrustedKeychainCertsOnly(t *testing.T) {
	apple, _ := testCert(t, "Apple root")
	distrusted, distrustedSHA := testCert(t, "Distrusted root")
	corp, corpSHA := testCert(t, "Corporate TLS inspection")
	flock, flockSHA := testCert(t, "Flock CA")
	empty, emptySHA := testCert(t, "Empty settings")
	wifi, wifiSHA := testCert(t, "Wi-Fi only")
	host, hostSHA := testCert(t, "One host only")
	untouched, _ := testCert(t, "No trust settings at all")
	userDenied, userDeniedSHA := testCert(t, "Admin trusts, user denies")

	admin := trustPlist(map[string]string{corpSHA: sslRoot, flockSHA: "", userDeniedSHA: sslRoot, distrustedSHA: sslDeny})
	user := trustPlist(map[string]string{strings.ToLower(emptySHA): emptyArr, wifiSHA: eapOnly, hostSHA: oneHost, userDeniedSHA: sslDeny})
	decisions := trustDecisions(admin, nil, []byte("not a plist"), user)

	base := append(append([]byte("Apple's bundle\n"), apple...), distrusted...)
	keychains := bytes.Join([][]byte{corp, flock, empty, wifi, host, untouched, userDenied, apple}, nil)
	got := string(mergeRoots(base, keychains, decisions))

	for name, cert := range map[string][]byte{"apple": apple, "corp": corp, "flock": flock, "empty": empty} {
		if strings.Count(got, string(cert)) != 1 {
			t.Errorf("%s: want once in the bundle", name)
		}
	}
	for name, cert := range map[string][]byte{"distrusted": distrusted, "wifi": wifi, "host": host, "untouched": untouched, "userDenied": userDenied} {
		if strings.Contains(got, string(cert)) {
			t.Errorf("%s must not be in the bundle", name)
		}
	}
	if !strings.HasPrefix(got, string(apple)) {
		t.Error("the system bundle's certificates must come first")
	}
}

func TestSystemRootsIncludesAppleRoots(t *testing.T) {
	roots := SystemRoots()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(roots) || bytes.Count(roots, []byte("BEGIN CERTIFICATE")) < 50 {
		t.Fatalf("expected Apple's roots, got %d bytes", len(roots))
	}
}
