package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Domain is one of a site's extra domains (Site.Domains), stored structured so its kind is
// explicit and never inferred from the suffix a stored string happens to end in: after the
// global TLD becomes .loc, a stored "demo2.loc" string would read as an inherited alias and
// the next global change would drag it along (the Q2 trap). The marker says what follows and
// what is absolute:
//
//   - {label:"tenant1"} — a subdomain of the site's domain: tenant1.<site domain>. The label
//     may itself be multi-label ("a.b"), as subdomain entries always were.
//   - {label:"app-2", tld:"", alias:true} — an alias under the site's effective suffix: it
//     follows the site (and, while the site inherits, the global TLD) — exactly Plan 1's
//     follow-the-global behavior for legacy "app-2.test" strings.
//   - {label:"demo2", tld:"loc", alias:true} — an alias under a suffix of its own: absolute,
//     never rewritten by a global or site suffix change.
type Domain struct {
	Label string `json:"label"`
	// TLD is the alias's own suffix; "" means the site's effective suffix (see Site.Suffix).
	// It is only meaningful with Alias; a subdomain carries no suffix at all.
	TLD string `json:"tld,omitempty"`
	// Alias marks a name of its own (served with its subdomains) rather than a subdomain of
	// the site's domain.
	Alias bool `json:"alias,omitempty"`

	// legacy is a plain string entry as read from an old config.json, kept verbatim for the
	// one-time migration at Load (see migrate): only Load knows the global TLD the string's
	// suffix must be compared against. Unexported, so it never reaches the structured form
	// a Save writes.
	legacy string
}

// UnmarshalJSON accepts both wire forms: the structured object, and the legacy plain string
// (old config.json files, old CLIs and apps) which it keeps for migrate to classify.
func (d *Domain) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		*d = Domain{legacy: raw}
		return nil
	}
	var v struct {
		Label string `json:"label"`
		TLD   string `json:"tld"`
		Alias bool   `json:"alias"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("a domain must be a string or an object, not %s", data)
	}
	*d = Domain{Label: v.Label, TLD: v.TLD, Alias: v.Alias}
	return nil
}

// migrate turns one entry read from config.json into its stored form, given the (sanitized)
// global TLD. A legacy string ending in "."+tld was an alias under the then only suffix →
// an inherited alias with the suffix cut off (Plan 1's follow-semantics, preserved exactly);
// every other legacy string ("tenant1", "a.b", "x.example") is the subdomain label it always
// was, so the hosts after migration are byte-identical to today's. Structured entries
// survive, with their suffix sanitized: a bad Domain.TLD falls back to inherit, and a
// subdomain never carries one.
func (d *Domain) migrate(globalTLD string) {
	if d.legacy != "" {
		if label, ok := strings.CutSuffix(d.legacy, "."+globalTLD); ok {
			*d = Domain{Label: label, Alias: true}
		} else {
			*d = Domain{Label: d.legacy}
		}
		return
	}
	if d.Alias {
		d.TLD = sanitizeSuffix(d.TLD) // "" = inherit, like an invalid Site.TLD
	} else {
		d.TLD = ""
	}
}

// sanitizeSuffix is Plan 1's rule for a stored suffix nobody screened (config.json is
// hand-editable): normalize it, and anything ValidateTLD refuses becomes "" — inherit. An
// override that would be refused must never be worse than no override.
func sanitizeSuffix(raw string) string {
	tld := NormalizeTLD(raw)
	if _, err := ValidateTLD(tld); err != nil {
		return ""
	}
	return tld
}

// Suffix is the suffix the site is served under: its own TLD override, the global Config.TLD
// when it inherits (Site.TLD == "").
func (s Site) Suffix(c *Config) string {
	if s.TLD != "" {
		return s.TLD
	}
	return c.TLD
}

// ActiveSuffixes lists every suffix Flock has to provision for: the global TLD, the sites'
// overrides and the aliases' own suffixes — deduplicated, sorted, and only values ValidateTLD
// accepts (so public suffixes, which the model refuses to store, can never enter the set even
// through a config built in memory). The per-suffix provisioning (resolvers, NRPT, roots)
// syncs exactly this set.
func (c *Config) ActiveSuffixes() []string {
	set := map[string]struct{}{}
	add := func(raw string) {
		tld := NormalizeTLD(raw)
		if tld == "" {
			return
		}
		if _, err := ValidateTLD(tld); err != nil {
			return
		}
		set[tld] = struct{}{}
	}
	add(c.TLD)
	for _, s := range c.Sites {
		add(s.TLD)
		for _, d := range s.Domains {
			add(d.TLD)
		}
	}
	out := make([]string, 0, len(set))
	for tld := range set {
		out = append(out, tld)
	}
	slices.Sort(out)
	return out
}
