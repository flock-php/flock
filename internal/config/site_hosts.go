package config

// SiteHostCollision reports the first host the site — in the state given, its TLD override
// as proposed — is served at that Flock already serves for someone else: another site's
// domain, any site's alias (pinned hosts included), a proxy's domain. owner names the
// holder ("site shop", "proxy api"). "" for host means the landing is free.
//
// This is the config-side check the CLI's `flock site tld` and the API's
// PATCH /v1/sites/{name} {"tld":…} share: a suffix change moves the site's domain and its
// inherited aliases (pinned aliases stay where they were put), and none of the site's
// effective hosts may land on a name Flock already serves. The shape mirrors
// NormalizeDomain's conflicts.
func (c *Config) SiteHostCollision(site Site) (host, owner string) {
	taken := map[string]string{}
	for _, o := range c.AllSites() {
		if o.Name == site.Name {
			continue
		}
		taken[c.Domain(o)] = "site " + o.Name
		for _, e := range c.ExtraDomains(o) {
			if e.Alias {
				taken[e.Host] = "site " + o.Name
			}
		}
	}
	for _, p := range c.Proxies {
		taken[c.ProxyDomain(p)] = "proxy " + p.Name
	}

	hosts := []string{c.Domain(site)}
	for _, e := range c.ExtraDomains(site) {
		hosts = append(hosts, e.Host)
	}
	for _, h := range hosts {
		if holder, ok := taken[h]; ok {
			return h, holder
		}
	}
	return "", ""
}
