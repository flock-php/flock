package dns

import (
	"fmt"
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
		if !validTLD(tld) {
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

// validTLD accepts what a NRPT namespace and a resolver filename can both carry.
func validTLD(tld string) bool {
	if tld == "" || len(tld) > 63 {
		return false
	}
	for _, r := range tld {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
