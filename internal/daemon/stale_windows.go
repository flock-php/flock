//go:build windows

package daemon

import (
	"flock/internal/dns"
)

// staleSuffixes lists the suffixes Flock's NRPT rules send to 127.0.0.1 that are no longer
// active, merged with the root stores under <root>/certs/roots whose suffix left the set
// (certutil store entries of a retired suffix are as stale as stale rules; the CLI sweep
// retires them, the warning names them). The rule query needs no administrator rights; a
// query that fails says nothing (no warning), the wildcard check covers rules that don't
// work.
func staleSuffixes(root, global string, active []string) []string {
	return mergedStale(dns.StaleNRPTRules(active), staleRootSuffixes(root, global, active))
}
