package daemon

import (
	"context"
	"crypto/sha1"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"flock/internal/api"
	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/php"
)

// TestBundleCarriesEveryActiveSuffixRoot is the point of one root per suffix for the tools that
// verify with a bundle: PHP and Node read <root>/certs/cacert.pem, so every Flock root has to be
// in it — a .loc site's own leaf is signed by the .loc root, and a bundle carrying only the
// global root would make PHP reject it.
func TestBundleCarriesEveryActiveSuffixRoot(t *testing.T) {
	root, cfg := testRoot(t, nil) // the global suffix is "test"

	site := func(name string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	cfg.Upsert(config.Site{Name: "app", Path: site("app"), Secured: true, TLD: "loc"})
	cfg.Upsert(config.Site{Name: "shop", Path: site("shop"), Secured: true})
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	client := startDaemon(t, root)
	status, err := client.Reload(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]api.SiteStatus{}
	for _, s := range status.Sites {
		byName[s.Name] = s
	}
	if got := byName["app"].Domain; got != "app.loc" {
		t.Errorf("the site on its own suffix is served at %q, want app.loc", got)
	}
	if got := byName["shop"].Domain; got != "shop.test" {
		t.Errorf("the site that follows the global suffix is served at %q, want shop.test", got)
	}

	// The global root stayed at the legacy path; the override got its own under roots/.
	global, err := certs.FileFingerprint(filepath.Join(root, "certs", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	loc, err := certs.FileFingerprint(filepath.Join(root, "certs", "roots", "loc", "ca.crt"))
	if err != nil {
		t.Fatalf("no root for the .loc suffix: %v", err)
	}
	if global == loc {
		t.Fatal("the two suffixes share one root")
	}

	inBundle := bundleFingerprints(t, php.CABundle(root))
	for _, want := range []string{global, loc} {
		if !slices.Contains(inBundle, want) {
			t.Errorf("the PHP/Node CA bundle lacks the root %s: %v", want, inBundle)
		}
	}

	// A second reload keeps the roots it created (same fingerprints) and the bundle current.
	if _, err := client.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again, err := certs.FileFingerprint(filepath.Join(root, "certs", "roots", "loc", "ca.crt")); err != nil || again != loc {
		t.Errorf("the .loc root moved across a reload: %s -> %s (%v)", loc, again, err)
	}
	if got := bundleFingerprints(t, php.CABundle(root)); !slices.Equal(got, inBundle) {
		t.Errorf("the bundle changed across an unchanged reload: %v, want %v", got, inBundle)
	}
}

// bundleFingerprints lists the SHA-1 of every certificate in a PEM bundle, in file order.
func bundleFingerprints(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var fingerprints []string
	for rest := data; ; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			fingerprints = append(fingerprints, fmt.Sprintf("%X", sha1.Sum(block.Bytes)))
		}
		rest = next
	}

	return fingerprints
}
