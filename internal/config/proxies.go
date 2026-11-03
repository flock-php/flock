package config

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Proxy is a name served by a server of its own rather than by PHP, e.g. a Node.js dev server or
// an object store: <name>.<tld> and its subdomains (up to MaxDepth levels) are passed to Upstream,
// WebSocket upgrades included. The Host header passes unchanged.
type Proxy struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream"` // as NormalizeUpstream returns it, e.g. "http://127.0.0.1:3000"
	Secured  bool   `json:"secured"`
}

// ProxyDomain is the hostname of a proxy.
func (c *Config) ProxyDomain(p Proxy) string {
	return p.Name + "." + c.TLD
}

// NormalizeUpstream checks the server a proxy passes requests to and returns it as stored: a port
// ("3000") means 127.0.0.1, a host and port ("localhost:5173") plain HTTP, and a URL may name
// https ("https://127.0.0.1:8443"). Paths aren't supported: the request's path passes unchanged.
func NormalizeUpstream(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("the upstream is empty: give a port (3000), host:port or URL")
	}
	if port, err := strconv.Atoi(s); err == nil {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("%d is not a port", port)
		}
		return "http://127.0.0.1:" + strconv.Itoa(port), nil
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}

	u, err := url.Parse(s)
	switch {
	case err != nil:
		return "", fmt.Errorf("%q is not a port, host:port or URL", s)
	case u.Scheme != "http" && u.Scheme != "https":
		return "", fmt.Errorf("%s: only http and https upstreams are supported", s)
	case u.Hostname() == "":
		return "", fmt.Errorf("%s has no host", s)
	case u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/"):
		return "", fmt.Errorf("%s: give only the scheme, host and port — the request's path passes unchanged", s)
	}
	if p := u.Port(); p != "" {
		if port, err := strconv.Atoi(p); err != nil || port < 1 || port > 65535 {
			return "", fmt.Errorf("%s: %s is not a port", s, p)
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

// FindProxy looks a proxy up by name.
func (c *Config) FindProxy(name string) (Proxy, bool) {
	for _, p := range c.Proxies {
		if p.Name == name {
			return p, true
		}
	}
	return Proxy{}, false
}

// UpsertProxy adds or replaces a proxy by name. Its name can't be a linked site's: the domain
// would be served twice. A directory of the same name in a parked path is served by the proxy
// instead (see AllSites), as a Node.js app in the projects folder usually is.
func (c *Config) UpsertProxy(p Proxy) error {
	if !hostLabel.MatchString(p.Name) {
		return fmt.Errorf("%q is not a valid name: it may only contain a-z, 0-9 and inner hyphens", p.Name)
	}
	if slices.ContainsFunc(c.Sites, func(s Site) bool { return s.Name == p.Name }) {
		return fmt.Errorf("%s is a linked site's name — unlink the site first, or choose another name", p.Name)
	}
	domain := c.ProxyDomain(p)
	for _, s := range c.AllSites() {
		for _, e := range c.ExtraDomains(s) {
			if e.Alias && e.Host == domain {
				return fmt.Errorf("%s is already served by %s", domain, s.Name)
			}
		}
	}
	for i := range c.Proxies {
		if c.Proxies[i].Name == p.Name {
			c.Proxies[i] = p
			return nil
		}
	}
	c.Proxies = append(c.Proxies, p)
	return nil
}

// RemoveProxy deletes a proxy by name and reports whether it existed.
func (c *Config) RemoveProxy(name string) bool {
	i := slices.IndexFunc(c.Proxies, func(p Proxy) bool { return p.Name == name })
	if i < 0 {
		return false
	}
	c.Proxies = slices.Delete(c.Proxies, i, i+1)
	return true
}
