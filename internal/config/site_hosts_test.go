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

// TLDMoveCollision is the probe the global suffix switch runs before touching anything:
// every site inheriting the global suffix rides onto the new one (pinned sites and pinned
// aliases stay put), proxies' derived domains ride with the global suffix, and no host may
// end up claimed twice by claims the move created.
func TestTLDMoveCollisionSelf(t *testing.T) {
	c := &Config{TLD: "test", Sites: []Site{{Name: "demo", Domains: []Domain{
		{Label: "demo2", TLD: "loc", Alias: true},
		{Label: "demo2", Alias: true},
	}}}}
	col := c.TLDMoveCollision("loc")
	if col == nil {
		t.Fatal("the move must report the self pair")
	}
	if col.Host != "demo2.loc" || col.Self != "demo" || col.Moved != "demo2" {
		t.Fatalf("got %+v, want demo2.loc/self demo/moved demo2", col)
	}
	// Onto .dev the follower lands on demo2.dev: clean.
	if col := c.TLDMoveCollision("dev"); col != nil {
		t.Errorf("onto dev must be clean, got %+v", col)
	}
}

func TestTLDMoveCollisionCross(t *testing.T) {
	// shop pins .loc; demo follows an alias "shop": onto .loc it lands on shop's host.
	c := &Config{TLD: "test", Sites: []Site{
		{Name: "demo", Domains: []Domain{{Label: "shop", Alias: true}}},
		{Name: "shop", TLD: "loc"},
	}}
	col := c.TLDMoveCollision("loc")
	if col == nil {
		t.Fatal("the move must report the cross collision")
	}
	if col.Host != "shop.loc" || col.Holder != "site shop" || col.Mover != "site demo" {
		t.Fatalf("got %+v, want shop.loc held by site shop, moved on by site demo", col)
	}

	// Both claims move: demo's inherited alias shop and shop's inherited primary.
	c2 := &Config{TLD: "test", Sites: []Site{
		{Name: "demo", Domains: []Domain{{Label: "shop", Alias: true}}},
		{Name: "shop"},
	}}
	if col := c2.TLDMoveCollision("loc"); col == nil || col.Host != "shop.loc" {
		t.Fatalf("mover-onto-mover must be refused, got %+v", col)
	}

	// A proxy's domain derives from the global suffix and rides with it.
	c3 := &Config{TLD: "test", Sites: []Site{{Name: "demo", Domains: []Domain{{Label: "api", Alias: true}}}},
		Proxies: []Proxy{{Name: "api", Upstream: "http://127.0.0.1:3000"}}}
	if col := c3.TLDMoveCollision("loc"); col == nil || col.Host != "api.loc" {
		t.Fatalf("alias onto a proxy's moved host must be refused, got %+v", col)
	}

	// A pair that collides identically before and after the move (both claims pinned)
	// predates the switch: status rows show it, an unrelated move must not be blocked.
	c4 := &Config{TLD: "test", Sites: []Site{
		{Name: "demo", Domains: []Domain{{Label: "shop", TLD: "loc", Alias: true}}},
		{Name: "shop", TLD: "loc"},
	}}
	if col := c4.TLDMoveCollision("dev"); col != nil {
		t.Errorf("a pre-existing pinned pair must not block a move, got %+v", col)
	}
}

func TestTLDMoveCollisionClean(t *testing.T) {
	// Plan 1's fixture shape: the site app-2 with the inherited alias app-2 (domain vs
	// alias, out of scope) and a plain inherited alias ride the move untouched.
	c := &Config{TLD: "test", Sites: []Site{{Name: "app-2", Domains: []Domain{
		{Label: "app-2", Alias: true}, {Label: "tenant1"}, {Label: "x.example"},
	}}}}
	for _, to := range []string{"loc", "dev2", "internal"} {
		if col := c.TLDMoveCollision(to); col != nil {
			t.Errorf("onto %s must be clean, got %+v", to, col)
		}
	}
}
