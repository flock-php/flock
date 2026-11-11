package dns

import (
	"fmt"
	"slices"
	"strings"
)

// The NRPT command layer is pure strings, shared so it can be tested on any OS: nrpt_windows.go
// runs the composed PowerShell, and nothing in here touches Windows.

// nrptComment marks Flock's rules, so they can be found and replaced.
const nrptComment = "Flock"

// removeRulesScript deletes every rule carrying Flock's comment.
const removeRulesScript = `Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '` + nrptComment + `' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }; `

// nrptSetScript composes the script that replaces Flock's rules with one per suffix, sending
// .<suffix> to Flock's server at 127.0.0.1, and clears the DNS cache.
func nrptSetScript(tlds []string) (string, error) {
	if len(tlds) == 0 {
		return "", fmt.Errorf("no suffixes to provision")
	}
	var b strings.Builder
	b.WriteString(removeRulesScript)
	for _, tld := range tlds {
		if !ValidTLD(tld) {
			return "", fmt.Errorf("invalid TLD %q", tld)
		}
		fmt.Fprintf(&b, `Add-DnsClientNrptRule -Namespace '.%s' -NameServers '127.0.0.1' -Comment '%s'; `, tld, nrptComment)
	}
	b.WriteString("Clear-DnsClientCache")
	return b.String(), nil
}

// nrptRuleNamespaces reads the output of the Flock-rule query (one "<namespaces>|<servers>" line
// per rule) into the namespaces Flock's rules send to 127.0.0.1.
func nrptRuleNamespaces(out string) map[string]bool {
	set := map[string]bool{}
	for line := range strings.Lines(out) {
		namespaces, servers, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || strings.TrimSpace(servers) != "127.0.0.1" {
			continue
		}
		for _, ns := range strings.Split(namespaces, ",") {
			if ns = strings.TrimSpace(ns); ns != "" {
				set[ns] = true
			}
		}
	}
	return set
}

// StaleNRPTRuleSuffixes reads that same Flock-rule dump and lists the single-label namespaces
// (".loc" → "loc") outside tlds: rules Flock provisioned for suffixes it no longer serves,
// which `flock install` sweeps away. Multi-label namespaces are nothing Flock added (its rules
// are one label each) and get no name. Sorted. The dump, never PowerShell, is what tests feed.
func StaleNRPTRuleSuffixes(out string, tlds []string) []string {
	active := normalizeSuffixes(tlds)
	var stale []string
	for ns := range nrptRuleNamespaces(out) {
		suffix := strings.TrimPrefix(ns, ".")
		if !ValidTLD(suffix) || slices.Contains(active, suffix) {
			continue
		}
		stale = append(stale, suffix)
	}
	slices.Sort(stale)
	return stale
}

// ValidTLD accepts what a NRPT namespace, a resolver file name and a resolved Domains= word can
// all carry: one DNS label — 1 to 63 characters of [a-z0-9-], not starting or ending with a
// hyphen (an edge hyphen names nothing resolvable). It is the allowlist behind parseTLDList.
func ValidTLD(tld string) bool {
	if tld == "" || len(tld) > 63 {
		return false
	}
	if tld[0] == '-' || tld[len(tld)-1] == '-' {
		return false
	}
	for _, r := range tld {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
