package daemon

import (
	"fmt"
	"net"

	"flock/internal/config"
)

// interfaceAddrs lists this machine's addresses. Tests replace it.
var interfaceAddrs = net.InterfaceAddrs

// listenAddresses are the config's listen addresses the web and DNS servers can use now: IPv4
// addresses of this machine, each once. The others are problems to report; one that isn't there
// yet (a VPN that connects later) is picked up by the retries of a degraded daemon.
func listenAddresses(cfg *config.Config) (hosts, problems []string) {
	if len(cfg.Listen) == 0 {
		return nil, nil
	}
	own := map[string]bool{}
	if addrs, err := interfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				own[n.IP.String()] = true
			}
		}
	}
	seen := map[string]bool{}
	for _, l := range cfg.Listen {
		ip := net.ParseIP(l)
		switch {
		case ip == nil || ip.To4() == nil:
			problems = append(problems, fmt.Sprintf("listen: %q isn't an IPv4 address", l))
		case ip.IsLoopback() || ip.IsUnspecified():
			problems = append(problems, fmt.Sprintf("listen: %s isn't an address other machines reach this one on (its tailnet address, say)", l))
		case !own[ip.String()]:
			problems = append(problems, fmt.Sprintf("listen: %s isn't an address of this machine (yet)", l))
		case seen[ip.String()]:
			// The DNS server would bind it twice, and the failed second bind takes down every
			// listener, loopback included.
		default:
			seen[ip.String()] = true
			hosts = append(hosts, ip.String())
		}
	}
	return hosts, problems
}
