package share

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/binaries"
)

func TestNormalizeAllowTakesAddressesAndDomainsOnly(t *testing.T) {
	got, err := NormalizeAllow([]string{" Bob@Acme.com ", "*@client.co.uk", "bob@acme.com", ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "bob@acme.com,*@client.co.uk" {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"--url=http://evil", "-x@y.z", "bob", "*@*", "@acme.com", "a@b", "bob@acme.com --x", "a b@c.de", "*.acme.com", "bob@-acme.com"} {
		if _, err := NormalizeAllow([]string{bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTunnelURLMatchesOnlyQuickTunnelAddresses(t *testing.T) {
	for line, want := range map[string]string{
		"2026-10-19T10:00:00Z INF |  https://quiet-river-lamp-x1.trycloudflare.com                |": "https://quiet-river-lamp-x1.trycloudflare.com",
		"INF Requesting new quick Tunnel on trycloudflare.com...":                                    "",
		"https://evil.com/?x=https://a.trycloudflare.com.evil.com":                                   "",
	} {
		got := ""
		if m := tunnelURL.FindStringSubmatch(line); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", line, got, want)
		}
	}
}

func TestLatestSkipsVersionsWithoutAllowedMail(t *testing.T) {
	p := binaries.Platform()
	if p == "" {
		t.Skip("no packages for this platform")
	}
	entry := func(build int, pre bool) map[string]binaries.Entry {
		return map[string]binaries.Entry{p: {Build: build, URL: "https://x/c.zip", SHA256: "00", Prerelease: pre}}
	}
	m := &binaries.Manifest{Packages: map[string]map[string]map[string]binaries.Entry{Package: {
		"2026.9.2": entry(1, false), "2026.10.0": entry(1, false), "2026.11.0": entry(1, true),
	}}}
	if v, ok := latest(m); !ok || v.Version != "2026.10.0" {
		t.Fatalf("latest = %v, %v", v.Version, ok)
	}
	m.Packages[Package] = map[string]map[string]binaries.Entry{"2026.9.2": entry(1, false)}
	if _, ok := latest(m); ok {
		t.Fatal("picked a cloudflared without --allowed-mail")
	}
}

func TestInstalledIgnoresVersionsTooOld(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"2026.8.0", "2026.9.3"} {
		dir := filepath.Join(Dir(root), v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, exe()), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := installed(root); got != "2026.9.3" {
		t.Fatalf("installed = %q", got)
	}
	if err := os.RemoveAll(filepath.Join(Dir(root), "2026.9.3")); err != nil {
		t.Fatal(err)
	}
	if got := installed(root); got != "" {
		t.Fatalf("installed = %q", got)
	}
}

func write(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestViteProjectsAndStaleBuilds(t *testing.T) {
	dir := t.TempDir()
	old, now := time.Now().Add(-time.Hour), time.Now()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"build":"vite build"},"devDependencies":{"laravel-vite-plugin":"^2"}}`, old)
	if UsesVite(dir) {
		t.Fatal("no vite.config yet")
	}
	write(t, filepath.Join(dir, "vite.config.js"), "", old)
	write(t, filepath.Join(dir, "resources", "css", "app.css"), "", old)
	write(t, filepath.Join(dir, "resources", "node_modules", "x.js"), "", now.Add(time.Hour))
	if !UsesVite(dir) || !Stale(dir) {
		t.Fatal("a Vite project without a build is stale")
	}
	write(t, Manifest(dir), "{}", now)
	if Stale(dir) {
		t.Fatal("a fresh build is stale")
	}
	write(t, filepath.Join(dir, "resources", "views", "welcome.blade.php"), "", now.Add(time.Minute))
	if !Stale(dir) {
		t.Fatal("a changed view (Tailwind reads them) doesn't make the build stale")
	}
	write(t, Manifest(dir), "{}", now.Add(2*time.Minute))
	write(t, filepath.Join(dir, "tailwind.config.js"), "", now.Add(3*time.Minute))
	if !Stale(dir) {
		t.Fatal("a changed Tailwind config doesn't make the build stale")
	}

	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"laravel-vite-plugin":"^2"}}`, old)
	if UsesVite(dir) {
		t.Fatal("no build script")
	}
}

func TestConfigCached(t *testing.T) {
	dir := t.TempDir()
	if ConfigCached(dir) {
		t.Fatal("cached without a cache")
	}
	write(t, filepath.Join(dir, "bootstrap", "cache", "config.php"), "<?php return [];", time.Now())
	if !ConfigCached(dir) {
		t.Fatal("not cached")
	}
}

func TestQRCodes(t *testing.T) {
	png, err := QRPNG("https://quiet-river-lamp.trycloudflare.com", 4)
	if err != nil || len(png) < 100 {
		t.Fatalf("QRPNG: %v (%d bytes)", err, len(png))
	}
	text, err := QRText("https://quiet-river-lamp.trycloudflare.com")
	if err != nil || !strings.Contains(text, "▀") {
		t.Fatalf("QRText: %v", err)
	}
}

func TestStartRefusesASecondShareWithOtherSettings(t *testing.T) {
	m := &Manager{root: t.TempDir(), shares: map[string]*share{}}
	m.shares["app"] = &share{Status: Status{Site: "app", State: Running, Allow: []string{"bob@acme.com"}}}
	if err := m.Start(Request{Site: "app", Allow: []string{"Bob@acme.com"}}); err != nil {
		t.Fatalf("the same share again: %v", err)
	}
	if err := m.Start(Request{Site: "app"}); !errors.Is(err, ErrShared) {
		t.Fatalf("another share: %v", err)
	}
}

func TestStartRefusesTheSameShareWithAnotherDebugMode(t *testing.T) {
	m := &Manager{root: t.TempDir(), shares: map[string]*share{}}
	m.shares["app"] = &share{Status: Status{Site: "app", State: Running, Debug: true}}
	if err := m.Start(Request{Site: "app", Debug: true}); err != nil {
		t.Fatalf("the same share again: %v", err)
	}
	if err := m.Start(Request{Site: "app"}); !errors.Is(err, ErrShared) {
		t.Fatalf("without debug mode: %v", err)
	}
}
