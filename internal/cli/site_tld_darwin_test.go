package cli

import (
	"slices"
	"testing"
)

// TestUntrustRootArgs pins the macOS untrust command construction: the trust decisions go
// first (admin domain, then the user's), both best-effort — a certificate nothing ever
// trusted has nothing to remove — and the certificate itself leaves the login keychain by
// SHA-1 fingerprint, the way untrustCA has always deleted the global root.
func TestUntrustRootArgs(t *testing.T) {
	got := untrustRootArgs("/root/certs/roots/loc/ca.crt", "AABBCCDDEEFF", "/Users/u/Library/Keychains/login.keychain-db")
	want := [][]string{
		{"security", "remove-trusted-cert", "-d", "/root/certs/roots/loc/ca.crt"},
		{"security", "remove-trusted-cert", "/root/certs/roots/loc/ca.crt"},
		{"security", "delete-certificate", "-Z", "AABBCCDDEEFF", "/Users/u/Library/Keychains/login.keychain-db"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d commands, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("command %d: got %v, want %v", i, got[i], want[i])
		}
	}
}
