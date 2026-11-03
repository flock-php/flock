package daemon

import (
	"net"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"flock/internal/api"
	"flock/internal/ports"
)

// proxyProbeTTL is how long a probe of a proxy's upstream is reused: the apps poll the status
// every few seconds, and naming the server asks lsof.
const proxyProbeTTL = 3 * time.Second

// serverProbe tells whether something answers at a proxy's upstream and, for one on this machine,
// what it is. Dev servers start and stop without Flock, so this is probed when the status is read
// rather than on reload.
type serverProbe struct {
	mu   sync.Mutex
	seen map[string]probed // upstream → its last probe
}

type probed struct {
	at      time.Time
	running bool
	server  string
}

// live returns proxies with Running and Server filled in.
func (p *serverProbe) live(root string, proxies []api.ProxyStatus) []api.ProxyStatus {
	out := make([]api.ProxyStatus, len(proxies))
	for i, ps := range proxies {
		ps.Running, ps.Server = p.get(root, ps.Upstream)
		out[i] = ps
	}
	return out
}

func (p *serverProbe) get(root, upstream string) (bool, string) {
	p.mu.Lock()
	if seen, ok := p.seen[upstream]; ok && time.Since(seen.at) < proxyProbeTTL {
		p.mu.Unlock()
		return seen.running, seen.server
	}
	p.mu.Unlock()

	running, server := probeUpstream(root, upstream)

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen == nil {
		p.seen = map[string]probed{}
	}
	p.seen[upstream] = probed{at: time.Now(), running: running, server: server}
	return running, server
}

// probeUpstream connects to upstream and, when it's on this machine, names the program listening.
func probeUpstream(root, upstream string) (bool, string) {
	u, err := url.Parse(upstream)
	if err != nil {
		return false, ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), 300*time.Millisecond)
	if err != nil {
		return false, ""
	}
	conn.Close()

	if !isLoopback(u.Hostname()) {
		return true, ""
	}
	n, _ := strconv.Atoi(port)
	for _, h := range ports.Holders(n) {
		if name := serverName(root, h); name != "" {
			return true, name
		}
	}
	return true, ""
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// serverName names what listens at a proxy's upstream: "Node.js 22.23.4" for one of Flock's
// Node.js versions (<root>/node/<version>/bin/node; Next.js renames its process "next-server",
// the executable stays), "Node.js" for another node, else the program's name.
func serverName(root string, h ports.Holder) string {
	if h.Path != "" {
		if rel, err := filepath.Rel(filepath.Join(root, "node"), h.Path); err == nil && !strings.HasPrefix(rel, "..") {
			if version, _, ok := strings.Cut(filepath.ToSlash(rel), "/"); ok && version != "" {
				return "Node.js " + version
			}
		}
	}
	name := h.Process
	if h.Path != "" {
		name = strings.TrimSuffix(filepath.Base(h.Path), ".exe")
	}
	if name == "node" {
		return "Node.js"
	}
	return name
}
