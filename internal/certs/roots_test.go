package certs

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rootDirAt is where a suffix's root lives: the global suffix keeps the legacy <dir>/ca.crt
// (the file the OS trust and `flock install` follow), every other suffix gets
// <dir>/roots/<suffix>/, created by the same Ensure.
func rootDirAt(dir, suffix, global string) string {
	if suffix == global {
		return dir
	}

	return filepath.Join(dir, "roots", suffix)
}

// permitted reports whether ca constrains names to ".suffix", the way create writes a root.
func permitted(ca *CA, suffix string) bool {
	return slices.Contains(ca.cert.PermittedDNSDomains, "."+suffix)
}

func TestEnsureLoadsOneRootPerSuffix(t *testing.T) {
	dir := t.TempDir()
	roots := OpenRoots(dir, "test")

	// ActiveSuffixes comes sorted, with the global suffix anywhere in it.
	if err := roots.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}

	if got := roots.Suffixes(); !slices.Equal(got, []string{"loc", "test"}) {
		t.Fatalf("Suffixes() = %v, want [loc test]", got)
	}

	all := roots.All()
	if len(all) != 2 {
		t.Fatalf("All() = %d roots, want 2", len(all))
	}
	loc, test := all[0], all[1] // sorted by suffix
	if loc.CertPath() != filepath.Join(rootDirAt(dir, "loc", "test"), "ca.crt") {
		t.Errorf("loc root at %s, want %s", loc.CertPath(), filepath.Join(dir, "roots", "loc", "ca.crt"))
	}
	if test.CertPath() != filepath.Join(dir, "ca.crt") {
		t.Errorf("the global root must stay at the legacy path, got %s", test.CertPath())
	}

	// Each root is name-constrained to its own suffix and to none of the others.
	if !permitted(loc, "loc") || permitted(loc, "test") {
		t.Errorf("loc root permits %v", loc.cert.PermittedDNSDomains)
	}
	if !permitted(test, "test") || permitted(test, "loc") {
		t.Errorf("test root permits %v", test.cert.PermittedDNSDomains)
	}
	if loc.Fingerprint() == test.Fingerprint() {
		t.Fatal("the two suffixes must not share one root")
	}
	if global := roots.Global(); global == nil || global.Fingerprint() != test.Fingerprint() {
		t.Errorf("Global() = %v, want the test root", global)
	}
}

// TestARootRefusesNamesUnderAnotherSuffix pins the point of one root per suffix: a leaf the
// .test root issues for a .loc name is signed (Go does not enforce the parent's constraints at
// signing time) but no verifier accepts it, because the root's name constraints are critical.
func TestARootRefusesNamesUnderAnotherSuffix(t *testing.T) {
	dir := t.TempDir()
	roots := OpenRoots(dir, "test")
	if err := roots.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}
	test, ok := roots.For("app.test")
	if !ok {
		t.Fatal("no root for app.test")
	}
	loc, ok := roots.For("demo.loc")
	if !ok {
		t.Fatal("no root for demo.loc")
	}

	testPool := x509.NewCertPool()
	testPool.AddCert(test.cert)
	locPool := x509.NewCertPool()
	locPool.AddCert(loc.cert)
	leaves := NewLeaves()

	// The .test root must not produce a usable certificate for a .loc name.
	cross, err := leaves.Get(test, "a.loc")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cross.Leaf.Verify(x509.VerifyOptions{DNSName: "a.loc", Roots: testPool})
	if err == nil {
		t.Fatal("the .test root's leaf for a.loc verified: name constraints are not enforced")
	}
	var invalid x509.CertificateInvalidError
	if !errors.As(err, &invalid) || invalid.Reason != x509.CANotAuthorizedForThisName {
		t.Fatalf("a.loc under the .test root: want a name-constraint violation, got %v", err)
	}
	if !strings.Contains(err.Error(), `"a.loc" is not permitted by any constraint`) {
		t.Fatalf("a.loc under the .test root: want the violation to name a.loc, got %v", err)
	}

	// The loc root signs the same names, and both its leaf shapes verify.
	deep, err := leaves.Get(loc, "x.demo.loc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deep.Leaf.Verify(x509.VerifyOptions{DNSName: "x.demo.loc", Roots: locPool}); err != nil {
		t.Errorf("x.demo.loc under the loc root: %v", err)
	}
	certPath, _, err := loc.SiteCert("demo.loc")
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(t, loc, certPath, "demo.loc"); err != nil {
		t.Errorf("the site certificate of demo.loc: %v", err)
	}
	// The file certificate lives inside its own suffix's root directory.
	if want := filepath.Join(dir, "roots", "loc", "sites", "demo.loc.crt"); certPath != want {
		t.Errorf("site certificate at %s, want %s", certPath, want)
	}
}

func TestEnsureAcrossReloadsIsStableAndKeepsWhatItLoaded(t *testing.T) {
	dir := t.TempDir()
	roots := OpenRoots(dir, "test")
	if err := roots.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, ca := range roots.All() {
		before[suffixOf(ca.CertPath(), dir)] = ca.Fingerprint()
	}

	// A reload re-Ensures the same set: the roots on disk are reused, so every fingerprint holds.
	if err := roots.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}
	for _, ca := range roots.All() {
		if got, want := ca.Fingerprint(), before[suffixOf(ca.CertPath(), dir)]; got != want {
			t.Fatalf("%s: fingerprint changed across a reload: %s -> %s", ca.CertPath(), want, got)
		}
	}

	// A new suffix joins, and the roots already loaded stay usable: a site whose override was
	// just removed keeps being served (its names are still trusted by the OS), and the bundle
	// keeps carrying its root so PHP verifies the leaves it already has.
	if err := roots.Ensure([]string{"dev", "loc", "test"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := roots.For("a.dev"); !ok {
		t.Fatal("no root for a.dev after it joined")
	}
	if err := roots.Ensure([]string{"test"}); err != nil {
		t.Fatal(err)
	}
	for _, ca := range roots.All() {
		s := suffixOf(ca.CertPath(), dir)
		if fp, seen := before[s]; seen && ca.Fingerprint() != fp {
			t.Errorf("%s: fingerprint changed: %s -> %s", s, fp, ca.Fingerprint())
		}
	}
	for _, name := range []string{"a.loc", "b.dev", "app.test"} {
		if _, ok := roots.For(name); !ok {
			t.Errorf("no root for %s after a reload that asked only for test", name)
		}
	}
}

// suffixOf names the suffix a root file belongs to, for readable assertions.
func suffixOf(certPath, dir string) string {
	rel, err := filepath.Rel(dir, certPath)
	if err != nil {
		return certPath
	}
	if rel == "ca.crt" {
		return "test" // the legacy path is the global suffix's in these tests
	}
	parts := strings.Split(rel, string(filepath.Separator)) // roots/<suffix>/ca.crt
	if len(parts) < 2 {
		return rel
	}

	return parts[1]
}

func TestForMatchesTheLongestSuffixOnLabelBoundaries(t *testing.T) {
	dir := t.TempDir()
	roots := OpenRoots(dir, "test")
	if err := roots.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"demo.loc", "x.demo.loc", "LOC", "Demo.LOC.", "loc"} {
		ca, ok := roots.For(name)
		if !ok {
			t.Fatalf("no root for %q", name)
		}
		if !permitted(ca, "loc") {
			t.Errorf("%q picked the root of %v, want the loc root", name, ca.cert.PermittedDNSDomains)
		}
	}
	// A suffix matches whole labels only: "loc" must not answer for xloc or block.
	for _, name := range []string{"xloc", "block", "locx", "a.xloc", "example.com", "com", "", "demo.lo"} {
		if _, ok := roots.For(name); ok {
			t.Errorf("%q picked a root, want no root", name)
		}
	}

	// A deeper suffix wins over a shorter one that also matches. Roots can only hold one-label
	// suffixes through the config, and For must still pick the longest name it has.
	if err := roots.Ensure([]string{"app.test"}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"a.app.test": ".app.test", "app.test": ".app.test", "shop.test": ".test"} {
		ca, ok := roots.For(name)
		if !ok || !slices.Contains(ca.cert.PermittedDNSDomains, want) {
			t.Errorf("%q picked %v, want the root permitting %s", name, ca.cert.PermittedDNSDomains, want)
		}
	}
}

// TestTheGlobalRootKeepsTheLegacyPathAcrossATLDChange pins what `flock tld loc` leaves behind:
// the global suffix's root is the file at <dir>/ca.crt, so switching the global suffix replaces
// it there (as it does today) rather than starting a second copy under roots/.
func TestTheGlobalRootKeepsTheLegacyPathAcrossATLDChange(t *testing.T) {
	dir := t.TempDir()
	if err := OpenRoots(dir, "test").Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}
	oldFingerprint, err := FileFingerprint(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}

	after := OpenRoots(dir, "loc")
	if err := after.Ensure([]string{"loc"}); err != nil {
		t.Fatal(err)
	}

	newFingerprint, err := FileFingerprint(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if newFingerprint == oldFingerprint {
		t.Fatal("the global root at ca.crt was not replaced when the global suffix moved to loc")
	}
	stored, err := readCert(filepath.Join(dir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(stored.PermittedDNSDomains, ".loc") {
		t.Fatalf("ca.crt permits %v, want it constrained to .loc", stored.PermittedDNSDomains)
	}
	if ca, ok := after.For("demo.loc"); !ok || ca.CertPath() != filepath.Join(dir, "ca.crt") {
		t.Fatalf("the loc root is at %v, want the legacy path", ca)
	}
	// The suffix that lost the global slot gets its own root, which Task 6's provisioning
	// sync is what trusts.
	if ca, ok := after.For("app.test"); ok {
		t.Errorf("test is not active and must not be loaded, got %s", ca.CertPath())
	}
	if err := after.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}
	if ca, ok := after.For("app.test"); !ok || ca.CertPath() != filepath.Join(dir, "roots", "test", "ca.crt") {
		t.Fatalf("the test root is at %v, want %s", ca, filepath.Join(dir, "roots", "test", "ca.crt"))
	}
}

func TestBundleCarriesEveryRoot(t *testing.T) {
	dir := t.TempDir()
	roots := OpenRoots(dir, "test")
	if err := roots.Ensure([]string{"loc", "test"}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "cacert.pem")
	const system = "SYSTEM ROOTS\n"
	if err := roots.Bundle(path, []byte(system)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), system) {
		t.Fatalf("the bundle must start with the system roots, got %q", firstLine(data))
	}

	// Every root's PEM follows, in All() order: PHP and Node verify their sites' leaves against
	// this file, so a root missing from it makes a site's own leaf untrusted.
	var blocks []*pem.Block
	rest := data
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		rest = next
	}
	all := roots.All()
	if len(blocks) != len(all) {
		t.Fatalf("the bundle carries %d root certificates, want %d", len(blocks), len(all))
	}
	for i, ca := range all {
		block, _ := pem.Decode([]byte(caPEM(t, ca)))
		if i >= len(blocks) || !slices.Equal(blocks[i].Bytes, block.Bytes) {
			t.Errorf("bundle certificate %d is not the root of %s", i, ca.CertPath())
		}
	}

	// Rewriting a bundle that is already current leaves the file alone.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := roots.Bundle(path, []byte(system)); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(again.ModTime()) {
		t.Error("Bundle rewrote a bundle that was already current")
	}
}

// caPEM is the PEM of a root's certificate, as it is stored at its CertPath.
func caPEM(t *testing.T, ca *CA) string {
	t.Helper()

	data, err := os.ReadFile(ca.CertPath())
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func firstLine(data []byte) string {
	if i := strings.IndexByte(string(data), '\n'); i >= 0 {
		return string(data[:i])
	}

	return string(data)
}
