package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"flock/internal/config"
)

// The project's Flock settings: ConfigFile is committed, LocalFile is this machine's and stays out
// of git.
const (
	ConfigFile = "flock.yml"
	LocalFile  = "flock.local.yml"
)

// SettingsFiles are the flock.yml and flock.local.yml files that apply to a site, in order: later
// files win. A git worktree reads its own flock.yml (it comes with the branch), its site's
// flock.local.yml (a new worktree has none of its own yet), then its own flock.local.yml.
func SettingsFiles(cfg *config.Config, site config.Site) []string {
	if site.IsWorktree() {
		if parent, ok := cfg.FindSite(site.Parent); ok {
			return []string{filepath.Join(site.Path, ConfigFile), filepath.Join(parent.Path, LocalFile), filepath.Join(site.Path, LocalFile)}
		}
	}
	return []string{filepath.Join(site.Path, ConfigFile), filepath.Join(site.Path, LocalFile)}
}

// CaddyFile is the file of Caddy directives a site's settings name with `caddyfile:`, and the
// settings file that names it; both are "" when none does. The last file that sets the key wins,
// so flock.local.yml replaces flock.yml, and an empty value turns it off. A relative path starts
// at the folder of the file that names it, ~/ at the home folder.
func CaddyFile(cfg *config.Config, site config.Site) (file, from string, err error) {
	for _, path := range SettingsFiles(cfg, site) {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", err
		}
		var settings struct {
			Caddyfile *string `yaml:"caddyfile"`
		}
		if err := yaml.Unmarshal(data, &settings); err != nil {
			return "", "", fmt.Errorf("%s: %w", path, err)
		}
		if settings.Caddyfile == nil {
			continue
		}
		file, from = strings.TrimSpace(*settings.Caddyfile), path
		switch {
		case file == "":
		case strings.HasPrefix(file, "~/") || strings.HasPrefix(file, `~\`):
			home, err := os.UserHomeDir()
			if err != nil {
				return "", "", fmt.Errorf("%s: %w", path, err)
			}
			file = filepath.Join(home, file[2:])
		case !filepath.IsAbs(file):
			file = filepath.Join(filepath.Dir(path), file)
		}
	}
	if file == "" {
		return "", "", nil
	}
	return file, from, nil
}
