//go:build darwin || linux

package daemon

import (
	"runtime"

	"flock/internal/dns"
)

// staleSuffixes lists the suffixes provisioned in the system that are no longer active, the
// leftovers of a TLD change whose `flock install` never ran. The scan is unprivileged:
// macOS reads /etc/resolver for files carrying Flock's marker (foreign files are left alone),
// Linux reads the domains routed by Flock's own drop-in — and both merge the root stores
// under <root>/certs/roots whose suffix left the set (trusted roots outside the active set
// are as stale as stale routing; the CLI sweep retires them, the warning names them). Tests
// move the directories.
func staleSuffixes(root, global string, active []string) []string {
	var dnsSide []string
	if runtime.GOOS == "darwin" {
		dnsSide = dns.StaleResolvers(dns.ResolverDir, active)
	} else {
		dnsSide = dns.StaleResolved(dns.ResolvedFile(), active)
	}
	return mergedStale(dnsSide, staleRootSuffixes(root, global, active))
}
