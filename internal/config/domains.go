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

// ExtraDomain is one of a site's extra domains (Site.Domains).
type ExtraDomain struct {
	Entry string // as stored: "tenant1" or "my-app-2.test"
	Host  string // the full name: "tenant1.app.test" or "my-app-2.test"
	// Alias is a name of its own (serving its subdomains too) rather than a subdomain of the site.
	Alias bool
	// Problem is why the name is not served, e.g. another site uses it.
	Problem string
}

// ExtraDomains resolves a site's extra domains to full names.
func (c *Config) ExtraDomains(s Site) []ExtraDomain {
	out := make([]ExtraDomain, 0, len(s.Domains))
	for _, entry := range s.Domains {
		if strings.HasSuffix(entry, "."+c.TLD) {
			out = append(out, ExtraDomain{Entry: entry, Host: entry, Alias: true})
		} else {
			out = append(out, ExtraDomain{Entry: entry, Host: entry + "." + c.Domain(s)})
		}
	}
	return out
}

// DomainsBySite resolves the extra domains of sites (by site name) and marks those that can't be
// served: an alias that is another site's domain or lies under it, or that an earlier site
// (by name) already claimed. Validation normally prevents these, but parked directories appear
// without asking.
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
			if owner := c.ownerOf(extras[i].Host, s.Name, primary, aliases); owner != "" {
				extras[i].Problem = fmt.Sprintf("%s is already served by %s", extras[i].Host, owner)
				continue
			}
			aliases[extras[i].Host] = s.Name
		}
		out[s.Name] = extras
	}
	return out
}

// ownerOf names the other site serving host: as its domain, an alias, or a subdomain of either,
// or with an alias under host (host's subdomains would take it over).
func (c *Config) ownerOf(host, site string, primary, aliases map[string]string) string {
	for _, names := range []map[string]string{primary, aliases} {
		for name, owner := range names {
			if owner == site {
				continue
			}
			if host == name || strings.HasSuffix(host, "."+name) || strings.HasSuffix(name, "."+host) {
				return owner
			}
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

// NormalizeDomain checks name as a new extra domain for site s and returns it as stored:
// a subdomain of the site's domain ("tenant1", "a.b"; "tenant1.app.test" is stored as "tenant1")
// or an alias under the TLD ("my-app-2.test").
func (c *Config) NormalizeDomain(s Site, name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	domain := c.Domain(s)

	switch {
	case name == "":
		return "", fmt.Errorf("the domain is empty")
	case strings.Contains(name, "*"):
		return "", fmt.Errorf("wildcards aren't needed: every subdomain up to %d levels below %s is served already — add a name only to put it into the hosts file", MaxDepth, domain)
	case strings.Contains(name, "://") || strings.ContainsAny(name, "/: "):
		return "", fmt.Errorf("%q is not a domain name (leave out the scheme, port and path)", name)
	case name == domain:
		return "", fmt.Errorf("%s is the site's own domain", name)
	case len(name) > 253:
		return "", fmt.Errorf("%s is too long", name)
	}

	alias := false
	if rest, ok := strings.CutSuffix(name, "."+domain); ok {
		name = rest
	} else if strings.HasSuffix(name, "."+c.TLD) {
		alias = true
	}

	labels := strings.Split(name, ".")
	if alias {
		labels = labels[:len(labels)-1]
	}
	for _, l := range labels {
		if !hostLabel.MatchString(l) {
			return "", fmt.Errorf("%q is not a valid domain name: %q may only contain a-z, 0-9 and inner hyphens", name, l)
		}
	}
	if !alias && len(labels) > MaxDepth {
		return "", fmt.Errorf("%s.%s is %d levels below %s; Flock serves up to %d", name, domain, len(labels), domain, MaxDepth)
	}
	if slices.Contains(s.Domains, name) {
		return "", fmt.Errorf("%s is already an extra domain of %s", name, s.Name)
	}

	if alias {
		sites := c.AllSites()
		primary, aliases := map[string]string{}, map[string]string{}
		for _, o := range sites {
			primary[c.Domain(o)] = o.Name
			for _, e := range c.ExtraDomains(o) {
				if e.Alias {
					aliases[e.Host] = o.Name
				}
			}
		}
		if owner := c.ownerOf(name, s.Name, primary, aliases); owner != "" {
			return "", fmt.Errorf("%s is already served by %s", name, owner)
		}
	}
	return name, nil
}
