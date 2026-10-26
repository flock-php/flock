package daemon

import (
	"net"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

func TestListenAddressesKeepsThisMachinesIPv4Addresses(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("100.64.0.7"), Mask: net.CIDRMask(32, 32)},
		}, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })

	if hosts, problems := listenAddresses(&config.Config{}); hosts != nil || problems != nil {
		t.Fatalf("no listen addresses: %v, %v", hosts, problems)
	}

	hosts, problems := listenAddresses(&config.Config{Listen: []string{"100.64.0.7", "100.64.0.8", "127.0.0.1", "0.0.0.0", "fd7a::1", "tailnet"}})
	if !slices.Equal(hosts, []string{"100.64.0.7"}) {
		t.Errorf("hosts %v, want the tailnet address only", hosts)
	}
	for _, want := range []string{"100.64.0.8 isn't an address of this machine", "127.0.0.1 isn't an address other machines reach", "0.0.0.0 isn't an address other machines reach", `"fd7a::1" isn't an IPv4 address`, `"tailnet" isn't an IPv4 address`} {
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
			t.Errorf("no problem %q in %q", want, problems)
		}
	}
}

func TestListenAddressesListsEachAddressOnce(t *testing.T) {
	old := interfaceAddrs
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("100.64.0.7"), Mask: net.CIDRMask(32, 32)}}, nil
	}
	t.Cleanup(func() { interfaceAddrs = old })

	// The DNS server would bind a repeated address twice and lose every listener on the failure.
	hosts, problems := listenAddresses(&config.Config{Listen: []string{"100.64.0.7", "::ffff:100.64.0.7", "100.64.0.7"}})
	if !slices.Equal(hosts, []string{"100.64.0.7"}) || problems != nil {
		t.Fatalf("hosts %v, problems %v", hosts, problems)
	}
}
