package config

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// MaxDepth is how many subdomain levels below a site's domain (and below each alias) are served:
// a.b.c.d.app.test reaches the app, e.a.b.c.d.app.test does not.
const MaxDepth = 4

var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ExtraDomain is one of a site's extra domains (Site.Domains), resolved for display and
// routing: Entry is the API-facing string (the bare label for a subdomain, the full host
// for an alias, inherited or pinned — like the stored strings of old), Host the name it is
// served at.
type ExtraDomain struct {
	Entry string // API-facing: "tenant1" (subdomain), "app-2.test" (inherited), "demo2.loc" (pinned)
	Host  string // the full name it is served at: "tenant1.app.test", "app-2.test", "demo2.loc"
	// Alias is a name of its own (serving its subdomains too) rather than a subdomain of the site.
	Alias bool
	// Problem is why the name is not served, e.g. another site uses it.
	Problem string
}

// ExtraDomains resolves a site's extra domains to full names. A subdomain lives under the
// site's domain (label + "." + <name>.<suffix>); an inherited alias (Domain.TLD == "") takes
// the site's effective suffix at use time, so it follows the site and, while the site
// inherits, the global TLD — exactly the old "app-2.test" string behavior; a pinned alias
// carries its own suffix and never moves (the Q2 rule).
func (c *Config) ExtraDomains(s Site) []ExtraDomain {
	suffix := s.Suffix(c)
	out := make([]ExtraDomain, 0, len(s.Domains))
	for _, d := range s.Domains {
		e := ExtraDomain{Alias: d.Alias}
		switch {
		case d.Alias && d.TLD != "": // pinned: absolute host
			e.Entry, e.Host = d.Label+"."+d.TLD, d.Label+"."+d.TLD
		case d.Alias: // inherited: its own name under the site's effective suffix
			e.Entry, e.Host = d.Label+"."+suffix, d.Label+"."+suffix
		default: // subdomain of the site's domain
			e.Entry, e.Host = d.Label, d.Label+"."+c.Domain(s)
		}
		out = append(out, e)
	}
	return out
}

// DomainsBySite resolves the extra domains of sites (by site name) and marks those that can't be
// served: an alias that is another site's domain or that an earlier site (by name) already
// claimed, and a subdomain that another site has as its alias. Validation normally prevents
// these, but parked directories appear without asking.
//
// An alias may lie under another site's domain or alias (accounts.shop.test next to shop.test):
// the most specific name wins, so that site keeps the rest of its subdomains.
func (c *Config) DomainsBySite(sites []Site) map[string][]ExtraDomain {
	primary := map[string]string{} // domain → site
	for _, s := range sites {
		primary[c.Domain(s)] = s.Name
	}
	aliases := map[string]string{} // alias → site

	out := map[string][]ExtraDomain{}
	for _, s := range sites {
		extras := c.ExtraDomains(s)
		for i := range extras {
			if !extras[i].Alias {
				continue
			}
			if owner := ownerOf(extras[i].Host, s.Name, primary, aliases); owner != "" {
				extras[i].Problem = fmt.Sprintf("%s is already served by %s", extras[i].Host, owner)
				continue
			}
			aliases[extras[i].Host] = s.Name
		}
		out[s.Name] = extras
	}
	// The alias wins over the subdomain: it is the more specific name. So does a worktree
	// (feature.app.test) over the same name added to its parent as a subdomain.
	for _, s := range sites {
		for i, e := range out[s.Name] {
			if owner := ownerOf(e.Host, s.Name, aliases, primary); !e.Alias && owner != "" {
				out[s.Name][i].Problem = fmt.Sprintf("%s is served by %s", e.Host, owner)
			}
		}
	}
	return out
}

// ownerOf names the other site that has host as its name in one of names.
func ownerOf(host, site string, names ...map[string]string) string {
	for _, m := range names {
		if owner, ok := m[host]; ok && owner != site {
			return owner
		}
	}
	return ""
}

// AddDomain normalizes name (see NormalizeDomain) and adds it to s.
func (c *Config) AddDomain(s *Site, name string) (ExtraDomain, error) {
	entry, err := c.NormalizeDomain(*s, name)
	if err != nil {
		return ExtraDomain{}, err
	}
	s.Domains = append(s.Domains, entry)
	extras := c.ExtraDomains(*s)
	return extras[len(extras)-1], nil
}

// RemoveDomain removes the extra domain name from s, given as stored or as its full name.
func (c *Config) RemoveDomain(s *Site, name string) bool {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	for i, e := range c.ExtraDomains(*s) {
		if name == e.Entry || name == e.Host {
			s.Domains = slices.Delete(s.Domains, i, i+1)
			return true
		}
	}
	return false
}

// NormalizeDomain checks name as a new extra domain for site s and returns the entry to
// store: a subdomain of the site's domain stored relative ("tenant1", "a.b";
// "tenant1.app.test" is stored as "tenant1"), an inherited alias under the site's effective
// suffix ("app-2.test" on a .test site → {label:"app-2", alias:true} — follows the site),
// or an alias pinned to a validated non-public suffix of its own ("demo2.loc" on a .test
// site → {label:"demo2", tld:"loc", alias:true} — never moves). Public suffixes and suffixes
// ValidateTLD refuses never become aliases: "x.com" stays a subdomain label, as it always
// was. A non-public suffix is taken at its word: "x.example" joins "demo2.loc" in pinning,
// because no rule can tell a user's chosen suffix from an example domain's; a legacy stored
// "x.example" is not reclassified — migration keeps it a subdomain, host byte-identical.
func (c *Config) NormalizeDomain(s Site, name string) (Domain, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	domain, suffix := c.Domain(s), s.Suffix(c)

	switch {
	case name == "":
		return Domain{}, fmt.Errorf("the domain is empty")
	case strings.Contains(name, "*"):
		return Domain{}, fmt.Errorf("wildcards aren't needed: every subdomain up to %d levels below %s is served already — add a name only to put it into the hosts file", MaxDepth, domain)
	case strings.Contains(name, "://") || strings.ContainsAny(name, "/: "):
		return Domain{}, fmt.Errorf("%q is not a domain name (leave out the scheme, port and path)", name)
	case name == domain:
		return Domain{}, fmt.Errorf("%s is the site's own domain", name)
	case len(name) > 253:
		return Domain{}, fmt.Errorf("%s is too long", name)
	}

	alias, tld := false, ""
	if rest, ok := strings.CutSuffix(name, "."+domain); ok {
		// A host under the site's own domain is a subdomain of it, stored relative.
		name = rest
	} else if i := strings.LastIndex(name, "."); i > 0 {
		// A full host ending in a suffix is an alias: the site's effective suffix makes it
		// inherited (it follows the site), any other validated non-public suffix pins it.
		// A public suffix ("x.com") or one ValidateTLD refuses ("a.b" → "b") makes neither:
		// the whole name is a subdomain label, as today.
		rest, last := name[:i], name[i+1:]
		switch {
		case last == suffix:
			alias = true
		case !IsPublicTLD(last):
			if _, err := ValidateTLD(last); err == nil {
				alias, tld = true, last
			}
		}
		if alias {
			name = rest
		}
	}

	labels := strings.Split(name, ".")
	for _, l := range labels {
		if !hostLabel.MatchString(l) {
			return Domain{}, fmt.Errorf("%q is not a valid domain name: %q may only contain a-z, 0-9 and inner hyphens", name, l)
		}
	}
	if !alias && len(labels) > MaxDepth {
		return Domain{}, fmt.Errorf("%s.%s is %d levels below %s; Flock serves up to %d", name, domain, len(labels), domain, MaxDepth)
	}

	// The resolved host: a subdomain's is under the site's domain, an alias's is its own
	// full name — under the site's effective suffix when inherited, under its own when pinned.
	host := name
	if alias {
		if tld != "" {
			host = name + "." + tld
		} else {
			host = name + "." + suffix
		}
	} else {
		host = name + "." + domain
	}
	// Only the exact name can be taken: under another site's name, the more specific name wins.
	primary, aliases, subdomains := map[string]string{}, map[string]string{}, map[string]string{}
	for _, o := range c.AllSites() {
		primary[c.Domain(o)] = o.Name
		for _, e := range c.ExtraDomains(o) {
			if e.Alias {
				aliases[e.Host] = o.Name
			} else {
				subdomains[e.Host] = o.Name
			}
		}
	}
	for _, p := range c.Proxies {
		primary[c.ProxyDomain(p)] = p.Name
	}
	for _, e := range c.ExtraDomains(s) {
		if e.Host == host {
			return Domain{}, fmt.Errorf("%s is already an extra domain of %s", host, s.Name)
		}
	}
	if !alias {
		if owner := ownerOf(host, s.Name, aliases, primary); owner != "" {
			return Domain{}, fmt.Errorf("%s is already served by %s", host, owner)
		}
	} else if owner := ownerOf(host, s.Name, primary, aliases, subdomains); owner != "" {
		return Domain{}, fmt.Errorf("%s is already served by %s", host, owner)
	}
	return Domain{Label: name, TLD: tld, Alias: alias}, nil
}
