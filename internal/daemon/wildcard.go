package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"runtime"
	"slices"
	"time"

	"flock/internal/dns"
)

// wildcardInterval is how often the wildcard check runs besides after each reload.
var wildcardInterval = time.Minute

// wildcardState is whether every name under the TLD resolves to Flock, and if not, why.
type wildcardState struct {
	ok      bool
	problem string
}

// recheckWildcard retries a DNS server that couldn't start (Windows: port 53 was taken) and checks again.
func (d *daemon) recheckWildcard() {
	d.mu.Lock()
	cfg, down := d.cfg, d.dns == nil
	d.mu.Unlock()
	if cfg != nil && down && runtime.GOOS == "windows" {
		d.reloadMu.Lock()
		_ = d.ensureDNS(cfg)
		d.reloadMu.Unlock()
	}
	d.checkWildcard()
}

// checkWildcard looks up a random name under the TLD through the system resolver, the way browsers
// and PHP do, and checks that the query reached Flock's DNS server. On Windows that only happens
// when the NRPT rule from `flock install` is in effect; Group Policy, VPN clients and security
// software can override it without Flock noticing otherwise.
func (d *daemon) checkWildcard() {
	if !d.checking.CompareAndSwap(false, true) {
		return
	}
	defer d.checking.Store(false)

	d.mu.Lock()
	cfg, srv, dnsErr := d.cfg, d.dns, d.dnsErr
	d.mu.Unlock()
	if cfg == nil {
		return
	}

	var state wildcardState
	switch {
	case srv == nil && dnsErr != nil:
		state.problem = fmt.Sprintf("Flock's DNS server isn't running (%v)", dnsErr)
	case srv == nil:
		state.problem = "Flock's DNS server isn't running"
	case runtime.GOOS != "windows":
		state.ok = true // /etc/resolver sends the TLD here; there is nothing else in the way
	case lookupReaches(srv, cfg.TLD):
		state.ok = true
	default:
		state.problem = windowsWildcardProblem(cfg.TLD)
	}

	d.mu.Lock()
	d.wildcard = state
	d.mu.Unlock()
}

// lookupReaches resolves a fresh name (so no cache answers it) and reports whether srv received it.
func lookupReaches(srv *dns.Server, tld string) bool {
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	name := "flock-check-" + hex.EncodeToString(nonce) + "." + tld
	seen := srv.Watch(name)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// PreferGo false: ask Windows' resolver (which applies NRPT rules), as browsers and PHP do.
	addrs, err := (&net.Resolver{PreferGo: false}).LookupHost(ctx, name)
	return seen() && err == nil && slices.Contains(addrs, "127.0.0.1")
}

func windowsWildcardProblem(tld string) string {
	switch {
	case !dns.NRPTInstalled(tld):
		return fmt.Sprintf("Windows' DNS rule for .%s isn't set up — run `flock install`", tld)
	case dns.PolicyManaged():
		return fmt.Sprintf("your organization manages DNS rules (Group Policy), so Windows ignores Flock's rule for .%s", tld)
	default:
		return fmt.Sprintf("Windows doesn't send .%s lookups to Flock (a VPN or security software may be overriding DNS)", tld)
	}
}
