//go:build !darwin && !linux && !windows

package daemon

// staleSuffixes: outside macOS, Windows and Linux there is no DNS provisioning to scan;
// the root stores under <root>/certs/roots are platform-independent, so a store outside
// the active set is still stale setup.
func staleSuffixes(root string, active []string) []string {
	return mergedStale(staleRootSuffixes(root, active))
}
