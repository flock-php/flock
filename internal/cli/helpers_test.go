package cli

import (
	"strings"
	"testing"
)

// parseTLDList is the privileged-setup --tlds argv guard: a root-run entry point, so the check
// is the dns.ValidTLD allowlist (one DNS label of [a-z0-9-], no edge hyphens, ≤63), not a list
// of banned characters. Every pinned reject below passed the old denylist (strings.ContainsAny
// "/. " + length): a newline, backslash, underscore, edge hyphen — each embeds into a
// root-written systemd-resolved drop-in, a resolver file name or an NRPT namespace.
func TestParseTLDListAcceptsLabelSuffixes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"loc", []string{"loc"}},
		{"test, loc", []string{"test", "loc"}},
		{"LOC", []string{"loc"}},
		{"loc,loc", []string{"loc"}},
		{" loc ,, loc ", []string{"loc"}},
		{"a1-9", []string{"a1-9"}},
		{strings.Repeat("a", 63), []string{strings.Repeat("a", 63)}},
	} {
		got, err := parseTLDList(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
				break
			}
		}
	}
}

func TestParseTLDListRejectsOutsideAllowlist(t *testing.T) {
	for _, in := range []string{
		"te\nst", // raw newline: Domains=~te + a new raw line in the root-written drop-in
		"a\\b",   // backslash
		"a_b",    // underscore is not a DNS label character
		"-x",     // leading hyphen
		"x-",     // trailing hyphen
		"/etc/x",
		"a.b", // not one label
		"a b", // embedded space survives trimming
		strings.Repeat("a", 64),
	} {
		if got, err := parseTLDList(in); err == nil {
			t.Errorf("%q: accepted as %q, must be refused", in, got)
		}
	}
	// Empty and separator-only lists provision nothing and say so.
	for _, in := range []string{"", ",", " , "} {
		if _, err := parseTLDList(in); err == nil {
			t.Errorf("%q: an empty list must error", in)
		}
	}
}
