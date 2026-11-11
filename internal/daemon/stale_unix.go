//go:build darwin || linux

package daemon

import (
	"runtime"

	"flock/internal/dns"
)

// staleSuffixes lists the suffixes provisioned in the system that are no longer active, the
// leftovers of a TLD change whose `flock install` never ran. The scan is unprivileged:
// macOS reads /etc/resolver for files carrying Flock's marker (foreign files are left alone),
// Linux reads the domains routed by Flock's own drop-in. Tests move the directories.
func staleSuffixes(active []string) []string {
	if runtime.GOOS == "darwin" {
		return dns.StaleResolvers(dns.ResolverDir, active)
	}
	return dns.StaleResolved(dns.ResolvedFile(), active)
}
