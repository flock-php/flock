package share

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"flock/internal/binaries"
)

// Package is cloudflared's name in the binaries manifest: Cloudflare's own build, mirrored and
// checked against its published checksum.
const Package = "cloudflared"

// MinVersion is the oldest cloudflared Flock runs: --allowed-mail (protected quick tunnels)
// arrived in 2026.9.3. An older one would start without the protection, so it is never used.
const MinVersion = "2026.9.3"

// ErrUnavailable means the manifest has no cloudflared for this platform.
var ErrUnavailable = errors.New("cloudflared isn't available for this platform yet")

// Dir is where cloudflared versions are installed, one directory each.
func Dir(root string) string { return filepath.Join(root, "cloudflared") }

func exe() string {
	if runtime.GOOS == "windows" {
		return "cloudflared.exe"
	}
	return "cloudflared"
}

// binary is an installed version's executable.
func binary(root, version string) string { return filepath.Join(Dir(root), version, exe()) }

// installed is the newest installed version that Flock may run, "" when there is none.
func installed(root string) string {
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		return ""
	}
	newest := ""
	for _, e := range entries {
		v := e.Name()
		if !e.IsDir() || binaries.CompareVersions(v, MinVersion) < 0 {
			continue
		}
		if _, err := os.Stat(binary(root, v)); err != nil {
			continue
		}
		if newest == "" || binaries.CompareVersions(v, newest) > 0 {
			newest = v
		}
	}
	return newest
}

// latest is the newest installable cloudflared the manifest has for this platform.
func latest(manifest *binaries.Manifest) (binaries.Version, bool) {
	for _, v := range manifest.Versions(Package, binaries.Platform()) {
		if v.Installable() && !v.Prerelease && binaries.CompareVersions(v.Version, MinVersion) >= 0 {
			return v, true
		}
	}
	return binaries.Version{}, false
}

// ensure returns cloudflared's executable, installing the newest published version when none is
// installed or a newer one is published. Older versions are removed after an install; one that
// can't be (Windows: still running) goes on the next.
func ensure(ctx context.Context, root string, catalog *binaries.Catalog, client *http.Client) (string, error) {
	have := installed(root)
	var manifest *binaries.Manifest
	if catalog != nil {
		manifest = catalog.Manifest()
		if manifest == nil {
			manifest, _ = catalog.Check(ctx)
		}
	}
	v, published := latest(manifest)
	if have != "" && (!published || binaries.CompareVersions(v.Version, have) <= 0) {
		return binary(root, have), nil
	}
	if !published {
		if manifest == nil {
			return "", fmt.Errorf("can't download cloudflared: the package list couldn't be fetched")
		}
		return "", ErrUnavailable
	}

	dir := filepath.Join(Dir(root), v.Version)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		return "", err
	}
	if err := binaries.Install(ctx, client, v.Entry, dir, nil); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("download cloudflared %s: %w", v.Version, err)
	}
	if _, err := os.Stat(binary(root, v.Version)); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("the cloudflared %s package has no %s", v.Version, exe())
	}

	entries, _ := os.ReadDir(Dir(root))
	for _, e := range entries {
		if e.Name() != v.Version {
			_ = os.RemoveAll(filepath.Join(Dir(root), e.Name()))
		}
	}
	return binary(root, v.Version), nil
}
