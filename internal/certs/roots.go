package certs

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
)

// Roots is Flock's set of name-constrained root certificates: one per suffix Flock serves sites
// under — the global TLD and every site's own override (config.Config.ActiveSuffixes). A root may
// only sign names inside its own suffix, so the .loc sites are served by a root that cannot
// impersonate a .test site, and the OS trust of one suffix never has to vouch for the others.
//
// The global suffix's root stays where it has always been, at dir (ca.crt and ca.key): that file
// is the one the OS trust flows, `flock install` and the Windows helper follow. Every other
// suffix gets its own directory under dir/roots/<suffix>/, created by the same Ensure, so nothing
// about an existing install moves.
//
// Roots is safe for concurrent use: a reload Ensures the set while TLS handshakes read it.
type Roots struct {
	dir    string                         // Flock's <root>/certs
	global string                         // the suffix whose root lives at dir itself
	loaded atomic.Pointer[map[string]*CA] // suffix → root; Ensure swaps the whole map
}

// OpenRoots opens the store at dir (Flock's <root>/certs) whose global suffix — the one rooted at
// dir itself — is global.
func OpenRoots(dir, global string) *Roots {
	return &Roots{dir: dir, global: normalizeSuffix(global)}
}

// Ensure loads a root for every suffix in tlds, creating the missing ones, and keeps the roots it
// already loaded: a suffix whose override was just removed is still served (its names are still
// trusted by the OS) and stays in the bundle, so PHP keeps verifying the leaves it has.
//
// A suffix that is already loaded is re-checked, the way a reload re-checked the single CA
// before: Ensure re-reads the stored root and replaces it when it stopped permitting its suffix
// or its key stopped pairing with its certificate. When a create fails the previous set stays
// loaded, so the daemon keeps serving what it had and retries on the next reload.
func (r *Roots) Ensure(tlds []string) error {
	loaded := maps.Clone(r.snapshot())

	for _, raw := range tlds {
		suffix := normalizeSuffix(raw)
		if suffix == "" {
			continue // not a suffix Roots can store: see normalizeSuffix
		}
		ca, err := Ensure(r.dirFor(suffix), suffix)
		if err != nil {
			return fmt.Errorf("certificate authority for .%s: %w", suffix, err)
		}
		loaded[suffix] = ca
	}
	r.loaded.Store(&loaded)

	return nil
}

// For returns the root whose suffix covers host, the longest match on label boundaries: the root
// of "loc" answers for "loc", "demo.loc" and "x.demo.loc", and never for "xloc" or "block".
// It reports false when no loaded root covers the name, which is the answer a name outside
// Flock's suffixes must get: a leaf signed by a root that does not permit its suffix verifies
// against nothing.
func (r *Roots) For(host string) (*CA, bool) {
	name := strings.TrimSuffix(strings.ToLower(host), ".")

	var (
		found *CA
		best  string
	)
	for suffix, ca := range r.snapshot() {
		if name != suffix && !strings.HasSuffix(name, "."+suffix) {
			continue
		}
		if len(suffix) > len(best) {
			found, best = ca, suffix
		}
	}

	return found, found != nil
}

// All lists the loaded roots sorted by suffix, so the bundle they are concatenated into is the
// same file across reloads.
func (r *Roots) All() []*CA {
	loaded := r.snapshot()
	suffixes := slices.Sorted(maps.Keys(loaded))
	all := make([]*CA, 0, len(suffixes))
	for _, suffix := range suffixes {
		all = append(all, loaded[suffix])
	}

	return all
}

// Suffixes lists the suffixes with a root, sorted: the names HTTPS answers for.
func (r *Roots) Suffixes() []string {
	return slices.Sorted(maps.Keys(r.snapshot()))
}

// Global is the root of the global suffix, the certificate at dir/ca.crt that the OS trust flows
// follow. It is nil when that suffix has no root loaded, which happens only when it is not active.
func (r *Roots) Global() *CA {
	return r.snapshot()[r.global]
}

// Bundle writes system (PEM roots, see SystemRoots) followed by every loaded root to path: the
// CA bundle PHP and Node verify with (see CA.WriteBundle for one root). Every root has to be in
// it: PHP on a .loc site checks a leaf signed by the .loc root, and a bundle with only the
// global root makes that leaf untrusted. The file is left alone when it is already current.
func (r *Roots) Bundle(path string, system []byte) error {
	return writeBundle(path, system, r.All())
}

// dirFor is where a suffix's root lives: the global suffix keeps the legacy path, every other
// suffix gets roots/<suffix>/ under dir.
func (r *Roots) dirFor(suffix string) string {
	if suffix == r.global {
		return r.dir
	}

	return filepath.Join(r.dir, "roots", suffix)
}

// snapshot is the loaded set. The stored map is never mutated after Store, so a reader that
// clones it (Ensure) or only reads it (For, All) races with nothing.
func (r *Roots) snapshot() map[string]*CA {
	if loaded := r.loaded.Load(); loaded != nil {
		return *loaded
	}

	return map[string]*CA{}
}

// normalizeSuffix turns a suffix into the key and directory name to use for it: trimmed,
// lowercase and with any leading dot stripped, the way config.NormalizeTLD stores it. Validation
// stays config's (ValidateTLD); roots only refuses what cannot name a directory, so a config
// edited by hand cannot point roots/ outside the store.
func normalizeSuffix(raw string) string {
	suffix := strings.TrimLeft(strings.ToLower(strings.TrimSpace(raw)), ".")
	if suffix == "" || strings.Contains(suffix, "..") || strings.ContainsAny(suffix, `/\`) {
		return ""
	}

	return suffix
}

// writeBundle concatenates the system roots and the PEM of each of roots, in order, into path,
// leaving the file alone when it is already current.
func writeBundle(path string, system []byte, roots []*CA) error {
	out := append([]byte{}, system...)
	for _, ca := range roots {
		root, err := os.ReadFile(ca.CertPath())
		if err != nil {
			return err
		}
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, root...)
	}

	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, out) {
		return nil
	}

	return os.WriteFile(path, out, 0o644)
}
