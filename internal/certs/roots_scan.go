package certs

import (
	"os"
	"path/filepath"
	"slices"

	"flock/internal/dns"
)

// StaleRootDirs lists the names of the root stores under dir/roots whose suffix is not in
// active, sorted: the stores Flock created for suffixes that left the set — including the
// suffixes that left through an unprivileged path (unlink, `flock domain remove`) that runs
// no sync. The CLI sweep retires the stores it names; the daemon's stale warning reports
// them. A name that is not a validated suffix — dns.ValidTLD, the shape Roots.Ensure
// creates and config.ActiveSuffixes guarantees — is never listed: a foreign directory
// inside the store is left alone, and a hand-built list cannot escape it.
func StaleRootDirs(dir string, active []string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, "roots"))
	if err != nil {
		return nil // no store (nothing provisioned yet): nothing stale
	}
	var stale []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !dns.ValidTLD(name) || slices.Contains(active, name) {
			continue
		}
		stale = append(stale, name)
	}
	slices.Sort(stale)
	return stale
}

// ActiveRootCerts lists the existing certs dir/roots/<suffix>/ca.crt for the suffixes in
// active, sorted by path: the provisioning set, the disk glob intersected with what the
// config says Flock serves sites under. A root outside the active set must never join a
// provisioning pass — the next install would re-trust a root its suffix already left. The
// global root is not among them: it lives at dir itself (it belongs to the store, not to
// one suffix), and callers pass it separately.
func ActiveRootCerts(dir string, active []string) []string {
	var paths []string
	for _, suffix := range active {
		if !dns.ValidTLD(suffix) {
			continue // not a suffix Roots can store: the layout guard, as in StaleRootDirs
		}
		path := filepath.Join(dir, "roots", suffix, "ca.crt")
		if _, err := os.Stat(path); err != nil {
			continue // not on disk (yet): nothing to provision
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}
