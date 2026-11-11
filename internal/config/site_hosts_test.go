package config

import "testing"

// SiteSelfCollision is the self half of the landing check: SiteHostCollision skips the site
// itself, so the pair a suffix MOVE creates inside one site — an inherited alias riding onto
// the host a pinned alias of the same site already holds — needs its own probe. A pinned +
// inherited pair of the same label is refused at add time; only a move brings them together.
func TestSiteSelfCollision(t *testing.T) {
	c := &Config{TLD: "test", Sites: []Site{{Name: "demo", Domains: []Domain{
		{Label: "demo2", TLD: "loc", Alias: true},
		{Label: "demo2", Alias: true},
	}}}}
	site := c.Sites[0]

	// Moving the site onto .loc: the inherited demo2 lands on the pinned demo2.loc.
	moving := Site{Name: site.Name, TLD: "loc", Domains: site.Domains}
	if host, pinned, moved := c.SiteSelfCollision(moving); host != "demo2.loc" || pinned != "demo2" || moved != "demo2" {
		t.Fatalf("move onto loc: got %q/%q/%q, want demo2.loc/demo2/demo2", host, pinned, moved)
	}

	// The site's current state (.test): the two hosts differ, the state is clean.
	if host, _, _ := c.SiteSelfCollision(site); host != "" {
		t.Errorf("the current state must be clean, got %q", host)
	}

	// Onto .dev the follower lands on demo2.dev, the pinned entry stays at demo2.loc: clean.
	if host, _, _ := c.SiteSelfCollision(Site{Name: site.Name, TLD: "dev", Domains: site.Domains}); host != "" {
		t.Errorf("onto dev must be clean, got %q", host)
	}

	// Domain-vs-alias stays out of scope: the site app-2 with the inherited alias app-2 is
	// the model's own shape, pinned by Plan 1's fixtures, and no move created it.
	d := &Config{TLD: "test", Sites: []Site{{Name: "app-2", Domains: []Domain{{Label: "app-2", Alias: true}}}}}
	if host, _, _ := d.SiteSelfCollision(Site{Name: "app-2", TLD: "loc", Domains: []Domain{{Label: "app-2", Alias: true}}}); host != "" {
		t.Errorf("domain-vs-alias must stay out of scope, got %q", host)
	}

	// Subdomains name hosts under the site's domain, a different space: no self pair.
	sub := &Config{TLD: "test", Sites: []Site{{Name: "demo", Domains: []Domain{{Label: "tenant1"}, {Label: "demo2", Alias: true}}}}}
	if host, _, _ := sub.SiteSelfCollision(Site{Name: "demo", TLD: "loc", Domains: sub.Sites[0].Domains}); host != "" {
		t.Errorf("subdomains cannot self-collide, got %q", host)
	}
}
