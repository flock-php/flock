package dns

import (
	"bytes"
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows/registry"
)

// Windows' resolver can only send a domain to another DNS server through a Name Resolution
// Policy Table rule, and only to port 53. `flock install` adds one rule per active suffix (with
// administrator rights) sending .<suffix> to Flock's server on 127.0.0.1:53, so every subdomain
// resolves, not only the names in the hosts file. Group Policy rules replace local ones, so on
// managed machines the rules may be ignored; the daemon checks whether lookups really arrive
// (see daemon's wildcard check).

// Port is where the server must listen on Windows: NRPT rules can't name a port.
const Port = 53

// policyKey holds NRPT rules deployed by Group Policy.
const policyKey = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`

func powershell(script string) ([]byte, error) {
	out, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		"$ErrorActionPreference = 'Stop'; "+script).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%v: %s", err, bytes.TrimSpace(out))
	}
	return out, nil
}

// SetNRPT replaces Flock's rules with one per suffix. It needs administrator rights.
func SetNRPT(tlds []string) error {
	script, err := nrptSetScript(tlds)
	if err != nil {
		return err
	}
	_, err = powershell(script)
	return err
}

// RemoveNRPT deletes Flock's rules. It needs administrator rights.
func RemoveNRPT() error {
	_, err := powershell(removeRulesScript + "Clear-DnsClientCache")
	return err
}

// nrptRulesQuery prints Flock's rules as one "<namespaces>|<servers>" line each, the dump
// nrptRuleNamespaces and StaleNRPTRuleSuffixes parse.
const nrptRulesQuery = `Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '` + nrptComment + `' } | ForEach-Object { ($_.Namespace -join ',') + '|' + ($_.NameServers -join ',') }`

// NRPTInstalled reports whether a Flock rule sends .<tld> to 127.0.0.1.
func NRPTInstalled(tld string) bool {
	out, err := powershell(nrptRulesQuery)
	if err != nil {
		return false
	}
	return nrptRuleNamespaces(string(out))["."+tld]
}

// StaleNRPTRules lists the suffixes Flock's NRPT rules route to 127.0.0.1 that are outside
// tlds: rules for suffixes Flock no longer serves, which `flock install` sweeps away.
func StaleNRPTRules(tlds []string) []string {
	out, err := powershell(nrptRulesQuery)
	if err != nil {
		return nil
	}
	return StaleNRPTRuleSuffixes(string(out), tlds)
}

// PolicyManaged reports whether Group Policy deploys NRPT rules, which make Windows ignore local ones.
func PolicyManaged() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, policyKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return false
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(1)
	return err == nil && len(names) > 0
}
