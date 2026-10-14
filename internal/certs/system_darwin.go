//go:build darwin

package certs

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"os"
	"os/exec"
	"strings"

	"howett.net/plist"
)

// SystemRoots returns the OS trust store as PEM, for tools (PHP) that cannot read it themselves:
// /etc/ssl/cert.pem (Apple's roots) plus the certificates an administrator or the user explicitly
// trusts for SSL in their keychains (a company's TLS-inspection CA, mkcert's, Flock's own),
// without the ones they explicitly distrust. Without the keychain (`security` failing) it is
// /etc/ssl/cert.pem alone.
func SystemRoots() []byte {
	base, _ := os.ReadFile("/etc/ssl/cert.pem")

	// User trust settings override admin ones (as in macOS' own evaluation).
	admin, _ := exportTrustSettings("-d")
	user, _ := exportTrustSettings()
	decisions := trustDecisions(admin, user)
	if len(decisions) == 0 {
		return base
	}

	// Trust settings name certificates by SHA-1; the certificates themselves are in the
	// keychains on the user's search list (login and System).
	keychains, err := exec.Command("/usr/bin/security", "find-certificate", "-a", "-p").Output()
	if err != nil {
		keychains = nil
	}
	return mergeRoots(base, keychains, decisions)
}

// exportTrustSettings returns the trust settings of a domain (none: the user's, "-d": admin) as
// a plist. A domain without trust settings is an error.
func exportTrustSettings(domain ...string) ([]byte, error) {
	f, err := os.CreateTemp("", "flock-trust-*.plist")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	_ = f.Close()
	defer os.Remove(path)

	args := append(append([]string{"trust-settings-export"}, domain...), path)
	if err := exec.Command("/usr/bin/security", args...).Run(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

type trust int

const (
	trustUnspecified trust = iota
	trustRoot              // trusted as a root for SSL
	trustDenied            // explicitly distrusted for SSL
)

// sslPolicy is the OID of Apple's SSL policy (1.2.840.113635.100.1.3), as trust settings store it.
var sslPolicy = []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x63, 0x64, 0x01, 0x03}

// trustDecisions reads exported trust settings plists, later ones overriding earlier ones, into
// an SSL trust decision per certificate (upper-case hex SHA-1). Unreadable plists are skipped.
func trustDecisions(plists ...[]byte) map[string]trust {
	decisions := map[string]trust{}
	for _, data := range plists {
		if len(data) == 0 {
			continue
		}
		var exported struct {
			TrustList map[string]map[string]any `plist:"trustList"`
		}
		if _, err := plist.Unmarshal(data, &exported); err != nil {
			continue
		}
		for hash, entry := range exported.TrustList {
			if d := sslTrust(entry); d != trustUnspecified {
				decisions[strings.ToUpper(hash)] = d
			}
		}
	}
	return decisions
}

// sslTrust evaluates one certificate's trust settings for SSL. No settings (or an empty list)
// means "always trust" as a root. Settings for other policies, for one app only or for one host
// only (which a PEM bundle can't express) don't count; a setting without a result trusts as root.
func sslTrust(entry map[string]any) trust {
	raw, ok := entry["trustSettings"]
	if !ok {
		return trustRoot
	}
	settings, _ := raw.([]any)
	if len(settings) == 0 {
		return trustRoot
	}

	decision := trustUnspecified
	for _, s := range settings {
		setting, ok := s.(map[string]any)
		if !ok {
			continue
		}
		if policy, ok := setting["kSecTrustSettingsPolicy"].([]byte); ok && !bytes.Equal(policy, sslPolicy) {
			continue
		}
		if _, ok := setting["kSecTrustSettingsApplication"]; ok {
			continue
		}
		if _, ok := setting["kSecTrustSettingsPolicyString"]; ok {
			continue
		}
		switch result := plistInt(setting["kSecTrustSettingsResult"], 1); result {
		case 1, 2: // TrustRoot, TrustAsRoot
			return trustRoot
		case 3: // Deny
			decision = trustDenied
		}
	}
	return decision
}

func plistInt(v any, def int64) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case uint64:
		return int64(n)
	}
	return def
}

// mergeRoots returns the certificates of base that aren't denied, followed by those of keychains
// that are trusted as roots and not in base yet, as PEM.
func mergeRoots(base, keychains []byte, decisions map[string]trust) []byte {
	var out bytes.Buffer
	seen := map[string]bool{}
	add := func(data []byte, include func(hash string) bool) {
		for {
			var block *pem.Block
			block, data = pem.Decode(data)
			if block == nil {
				return
			}
			if block.Type != "CERTIFICATE" {
				continue
			}
			sum := sha1.Sum(block.Bytes)
			hash := strings.ToUpper(hex.EncodeToString(sum[:]))
			if seen[hash] || !include(hash) {
				continue
			}
			seen[hash] = true
			_ = pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})
		}
	}
	add(base, func(hash string) bool { return decisions[hash] != trustDenied })
	add(keychains, func(hash string) bool { return decisions[hash] == trustRoot })
	return out.Bytes()
}

// TrustedByUser reports whether the user's trust settings trust the certificate with this SHA-1
// fingerprint (upper-case hex, as FileFingerprint returns it) as an SSL root.
func TrustedByUser(fingerprint string) bool { return trustedIn(fingerprint) }

// TrustedByAdmin is TrustedByUser for the admin (system-wide) trust settings.
func TrustedByAdmin(fingerprint string) bool { return trustedIn(fingerprint, "-d") }

func trustedIn(fingerprint string, domain ...string) bool {
	data, err := exportTrustSettings(domain...)
	if err != nil {
		return false
	}
	return trustDecisions(data)[strings.ToUpper(fingerprint)] == trustRoot
}
