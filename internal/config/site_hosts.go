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

// SiteSelfCollision is the self half of the landing check: SiteHostCollision skips the site
// itself, so the pair a suffix MOVE creates inside one site needs its own probe — an alias
// that follows the site onto the new suffix landing on the host a pinned alias of the same
// site already holds. Adding such a pair is refused at add time (NormalizeDomain's host
// conflict); only a move brings the two entries onto one host, and once it does, the site
// serves the host twice: `flock domain remove <host>` reports success while the surviving
// entry keeps serving it, and the name can be neither removed nor re-added. The site is
// given in its proposed state (its TLD override as asked), as in SiteHostCollision.
//
// pinned names the absolute entry's label, moved the label of the entry that followed the
// site. A site's domain overlapping an alias of the same label (the site app-2 with the
// alias app-2) is the model's own shape — it predates any move and only the site serves
// that host — so it stays out of scope, pinned by Plan 1's fixtures. Subdomains live under
// the site's domain, a name space aliases never enter.
func (c *Config) SiteSelfCollision(site Site) (host, pinned, moved string) {
	suffix := site.Suffix(c)
	byHost := map[string]Domain{}
	for _, d := range site.Domains {
		if !d.Alias {
			continue
		}
		s := suffix
		if d.TLD != "" {
			s = d.TLD
		}
		h := d.Label + "." + s
		if prev, ok := byHost[h]; ok {
			if prev.TLD == "" {
				pinned, moved = d.Label, prev.Label
			} else {
				pinned, moved = prev.Label, d.Label
			}
			return h, pinned, moved
		}
		byHost[h] = d
	}
	return "", "", ""
}
