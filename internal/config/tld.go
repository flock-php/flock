package config

import (
	"fmt"
	"regexp"
	"strings"
)

// tldLabel is what Flock accepts as a local TLD: one DNS label of 2 to 63 characters, starting
// with a letter and ending with a letter or digit. It is stricter than the generic hostLabel
// (see domains.go): a one-letter label is more likely a typo than a choice, and a label with an
// edge hyphen can't be a resolvable name.
var tldLabel = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// specialUseTLDs are labels RFC 6761 pins for special use. They are not in the IANA root zone,
// so the public list alone would let them through: localhost resolves by definition, and
// invalid never does — neither can name sites.
var specialUseTLDs = map[string]struct{}{"localhost": {}, "invalid": {}}

// NormalizeTLD turns user input into the label to store: it trims spaces, strips leading dots
// (people type ".loc" the way they see it in a URL) and lowercases. The result is validated,
// not assumed: an empty one is as inputable as any.
func NormalizeTLD(raw string) string {
	return strings.TrimLeft(strings.ToLower(strings.TrimSpace(raw)), ".")
}

// ValidateTLD takes a normalized TLD (see NormalizeTLD) and returns a warning to show when it
// is accepted — mDNS owns "local" — and an error when it must not be used: not a label, a
// special-use name, or a public TLD, under which sites would shadow real internet names.
func ValidateTLD(tld string) (string, error) {
	if tld == "" {
		return "", fmt.Errorf("the TLD is empty")
	}
	if strings.Contains(tld, ".") {
		return "", fmt.Errorf("%q is not one label: pick a single suffix, like loc", tld)
	}
	if !tldLabel.MatchString(tld) {
		return "", fmt.Errorf("%q is not a valid TLD: use a-z, 0-9 and inner hyphens, from 2 to 63 characters starting with a letter", tld)
	}
	if _, ok := specialUseTLDs[tld]; ok {
		return "", fmt.Errorf("%q is reserved for special use (RFC 6761): it can't name sites", tld)
	}
	if IsPublicTLD(tld) {
		return "", fmt.Errorf("%q is a public TLD: sites under .%s would shadow real internet names", tld, tld)
	}
	if tld == "local" {
		return ".local is owned by mDNS (Bonjour, RFC 6767): macOS may keep resolving it itself and browsers special-case it — .loc is the usual choice", nil
	}
	return "", nil
}
