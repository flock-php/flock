package daemon

import (
	"maps"
	"path/filepath"
	"slices"

	"flock/internal/certs"
)

// staleRootSuffixes lists the root stores under <root>/certs/roots whose suffix is no
// longer active, and the roots/<global>/ store left by an adoption (the global root lives
// at certs/ca.crt): the trusted roots Goal 4's rule retires (certs.StaleRootDirs, the
// scan the CLI sweep shares). Unprivileged — a directory read. mergedStale folds them
// into the DNS-side suffixes so one stale warning, deduped, names the whole stale set.
func staleRootSuffixes(root, global string, active []string) []string {
	return certs.StaleRootDirs(filepath.Join(root, "certs"), global, active)
}

// mergedStale merges the stale scans (DNS-side, roots-side) into the one sorted, deduped
// list the aggregated stale warning names.
func mergedStale(lists ...[]string) []string {
	set := map[string]bool{}
	for _, list := range lists {
		for _, suffix := range list {
			set[suffix] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(set))
}
