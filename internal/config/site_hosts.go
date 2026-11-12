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

// TLDMove names the first collision a global suffix switch to newTLD creates. For a self
// pair Self is the site claiming one host twice and Moved the label of the alias that
// followed it; for a cross claim Holder owns the host and Mover lands on it (in the
// SiteHostCollision style: "site shop", "proxy api").
type TLDMove struct {
	Host   string
	Self   string
	Moved  string
	Holder string
	Mover  string
}

// TLDMoveCollision is the landing probe of the global switch, run before anything is
// touched. Every site inheriting the global suffix rides onto newTLD together with its
// inherited aliases, its subdomains riding with its domain; every proxy's derived domain
// rides with the global suffix too; sites and aliases pinned to a suffix of their own stay
// where they were put. The switch is refused when a host ends up claimed twice and at
// least one of the claims is a moving one:
//
//   - a self pair inside one site (SiteSelfCollision on the site's prospective state):
//     the site would serve one host twice, and `domain remove` would report success while
//     the surviving entry keeps serving it;
//   - a moving name landing on a host a pinned site, any site's alias, a pinned alias,
//     the site's own domain, or any proxy holds — SiteHostCollision's taken-set, mirrored
//     at the set level because every site and every proxy moves at once.
//
// A pair that collides identically before and after the switch (both claims pinned)
// predates it — status rows already flag it — and must not block an unrelated move. Sites
// are probed in name order and each site's primary before its aliases, as SiteHostCollision
// builds its maps, so the refusal is deterministic.
func (c *Config) TLDMoveCollision(newTLD string) *TLDMove {
	for _, site := range c.AllSites() {
		if site.TLD == "" {
			probe := site
			probe.TLD = newTLD
			if host, _, moved := c.SiteSelfCollision(probe); host != "" {
				return &TLDMove{Host: host, Self: site.Name, Moved: moved}
			}
		}
	}

	type claim struct {
		owner string
		moved bool
	}
	claims := map[string]claim{}
	report := func(host, owner string, moved bool) *TLDMove {
		prev, seen := claims[host]
		if !seen {
			claims[host] = claim{owner, moved}
			return nil
		}
		switch {
		case !prev.moved && !moved:
			return nil // a pinned pair as it was: the switch creates nothing here
		case prev.moved && !moved:
			return &TLDMove{Host: host, Holder: owner, Mover: prev.owner}
		default:
			return &TLDMove{Host: host, Holder: prev.owner, Mover: owner}
		}
	}

	for _, site := range c.AllSites() {
		moves := site.TLD == ""
		suffix := site.TLD
		if moves {
			suffix = newTLD
		}
		owner := "site " + site.Name
		primary := site.HostName() + "." + suffix
		if col := report(primary, owner, moves); col != nil {
			return col
		}
		for _, d := range site.Domains {
			if !d.Alias {
				continue // a subdomain rides on its site's domain: the primary covers it
			}
			s := suffix
			if d.TLD != "" {
				s = d.TLD
			}
			h := d.Label + "." + s
			if h == primary {
				continue // domain-vs-alias is the model's own shape: see SiteSelfCollision
			}
			if col := report(h, owner, moves && d.TLD == ""); col != nil {
				return col
			}
		}
	}
	for _, p := range c.Proxies {
		if col := report(p.Name+"."+newTLD, "proxy "+p.Name, true); col != nil {
			return col
		}
	}
	return nil
}
