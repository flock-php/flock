package cli

import (
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/certs"
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

// untrustStoreRoots is the uninstall untrust scope: the global CA first (certs/ca.crt,
// never in suffixRootCerts), then every per-suffix root on disk, sorted — the whole disk
// set, active suffixes and stale ones alike, exactly what macOS and Windows uninstall
// already loop. Linux had been left with the global CA alone, leaving per-suffix roots
// trusted in the browsers' NSS databases.
func TestUntrustStoreRootsCoversEveryRoot(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "certs", "ca.crt")
	loc := filepath.Join(root, "certs", "roots", "loc", "ca.crt")
	old := filepath.Join(root, "certs", "roots", "old", "ca.crt")
	for _, tc := range []struct{ dir, tld string }{
		{filepath.Dir(global), "test"}, {filepath.Dir(loc), "loc"}, {filepath.Dir(old), "old"},
	} {
		if _, err := certs.Ensure(tc.dir, tc.tld); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	untrustStoreRoots(root, func(path string) error {
		got = append(got, path)
		return nil
	}, io.Discard)
	if want := []string{global, loc, old}; !slices.Equal(got, want) {
		t.Fatalf("untrust scope: got %v, want %v", got, want)
	}
}

// One stubborn certificate — a keychain refusal, a locked database — must not stop the
// sweep: every root is handed to the untrust function all the same, and each failure is
// reported where the user can act on it (as darwin's loop and windows' delstore loop do).
func TestUntrustStoreRootsIsBestEffort(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ dir, tld string }{
		{filepath.Join(root, "certs"), "test"},
		{filepath.Join(root, "certs", "roots", "loc"), "loc"},
		{filepath.Join(root, "certs", "roots", "old"), "old"},
	} {
		if _, err := certs.Ensure(tc.dir, tc.tld); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	var out strings.Builder
	untrustStoreRoots(root, func(path string) error {
		got = append(got, path)
		switch filepath.Base(filepath.Dir(path)) {
		case "certs": // the global root's directory: make its untrust refuse
			return errors.New("the keystore says no")
		case "old":
			return errors.New("the database is locked")
		}
		return nil
	}, &out)
	if len(got) != 3 {
		t.Fatalf("a refusing certificate must not stop the sweep, got calls %v", got)
	}
	msg := out.String()
	if !strings.Contains(msg, "the keystore says no") || !strings.Contains(msg, "the database is locked") {
		t.Errorf("both refusals must be reported, got:\n%s", msg)
	}
	if !strings.Contains(msg, ".loc") {
		t.Errorf("the root that did leave must be reported by suffix, got:\n%s", msg)
	}
}

// validGlobalCAPath is the root-run argv guard for privileged-setup --ca on Windows: the one
// certificate that goes into the machine's Root store must be Flock's global root in its store
// layout — absolute, named ca.crt, sitting in a directory named certs — the way --roots
// entries carry their own guard. Per-suffix roots ride --roots, never --ca.
func TestValidGlobalCAPath(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"/home/u/.flock/certs/ca.crt", true},
		{"/.flock/certs/ca.crt", true},
		{"/home/u/.flock/certs/ca.pem", false},
		{"home/u/.flock/certs/ca.crt", false},
		{"/home/u/.flock/certs", false},
		{"/home/u/.flock/certs/sub/ca.crt", false},
		{"/home/u/.flock/certs/roots/loc/ca.crt", false}, // a per-suffix root belongs to --roots
		{"", false},
	} {
		if got := validGlobalCAPath(tc.in); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
		}
	}
}
