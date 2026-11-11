//go:build windows

package daemon

import (
	"flock/internal/dns"
)

// staleSuffixes lists the suffixes Flock's NRPT rules send to 127.0.0.1 that are no longer
// active: rules of a TLD change whose `flock install` never ran. The rule query needs no
// administrator rights; a query that fails says nothing (no warning), the wildcard check
// covers rules that don't work.
func staleSuffixes(active []string) []string {
	return dns.StaleNRPTRules(active)
}
