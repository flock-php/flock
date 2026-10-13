package dns

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Windows' resolver can only send a domain to another DNS server through a Name Resolution
// Policy Table rule, and only to port 53. `flock install` adds one (with administrator rights)
// sending .<tld> to Flock's server on 127.0.0.1:53, so every subdomain resolves, not only the
// names in the hosts file. Group Policy rules replace local ones, so on managed machines the rule
// may be ignored; the daemon checks whether lookups really arrive (see daemon's wildcard check).

// Port is where the server must listen on Windows: NRPT rules can't name a port.
const Port = 53

// nrptComment marks Flock's rules, so they can be found and replaced.
const nrptComment = "Flock"

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

const removeRules = `Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '` + nrptComment + `' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }; `

// SetNRPT replaces Flock's rule with one for tld. It needs administrator rights.
func SetNRPT(tld string) error {
	if !validTLD(tld) {
		return fmt.Errorf("invalid TLD %q", tld)
	}
	_, err := powershell(removeRules +
		fmt.Sprintf(`Add-DnsClientNrptRule -Namespace '.%s' -NameServers '127.0.0.1' -Comment '%s'; Clear-DnsClientCache`, tld, nrptComment))
	return err
}

// RemoveNRPT deletes Flock's rules. It needs administrator rights.
func RemoveNRPT() error {
	_, err := powershell(removeRules + "Clear-DnsClientCache")
	return err
}

// NRPTInstalled reports whether Flock's rule sends .<tld> to 127.0.0.1.
func NRPTInstalled(tld string) bool {
	out, err := powershell(`Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '` + nrptComment + `' } | ForEach-Object { ($_.Namespace -join ',') + '|' + ($_.NameServers -join ',') }`)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "."+tld+"|127.0.0.1" {
			return true
		}
	}
	return false
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
