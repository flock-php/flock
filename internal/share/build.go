package share

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A share never proxies the Vite dev server (see web.Share): visitors get the production build in
// public/build, which Flock builds first when it is missing or older than the project's sources.

// viteConfigs are the names Vite looks for its config under.
var viteConfigs = []string{"vite.config.js", "vite.config.ts", "vite.config.mjs", "vite.config.mts", "vite.config.cjs", "vite.config.cts"}

// UsesVite reports whether a project builds its assets with Vite and Laravel's plugin and has a
// build script: the setup the share's rewrite of @vite's tags understands.
func UsesVite(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Scripts["build"] == "" {
		return false
	}
	if _, ok := pkg.DevDependencies["laravel-vite-plugin"]; !ok {
		if _, ok := pkg.Dependencies["laravel-vite-plugin"]; !ok {
			return false
		}
	}
	for _, name := range viteConfigs {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// Manifest is the build's manifest, which @vite reads and the share's rewrite maps tags with.
func Manifest(dir string) string { return filepath.Join(dir, "public", "build", "manifest.json") }

// errNewer stops the walk once a newer source is found.
var errNewer = errors.New("newer")

// Stale reports whether the build is missing or older than a source it is built from: anything
// under resources/, the Vite, Tailwind and PostCSS configs, package.json and the lock files.
func Stale(dir string) bool {
	info, err := os.Stat(Manifest(dir))
	if err != nil {
		return true
	}
	built := info.ModTime()
	newer := func(t time.Time) bool { return t.After(built) }

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(name == "package.json" || strings.HasSuffix(name, "-lock.json") || strings.HasSuffix(name, ".lock") ||
			name == "pnpm-lock.yaml" || strings.HasPrefix(name, "vite.config.") || strings.HasPrefix(name, "tailwind.config.") ||
			strings.HasPrefix(name, "postcss.config.")) {
			continue
		}
		if fi, err := e.Info(); err == nil && newer(fi.ModTime()) {
			return true
		}
	}

	err = filepath.WalkDir(filepath.Join(dir, "resources"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if fi, err := d.Info(); err == nil && newer(fi.ModTime()) {
			return errNewer
		}
		return nil
	})
	return errors.Is(err, errNewer)
}

// ConfigCached reports whether the app's config is cached (`php artisan config:cache`): Laravel
// then ignores the environment, so a share can't switch debug mode off for its visitors.
func ConfigCached(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "bootstrap", "cache", "config.php"))
	return err == nil
}
