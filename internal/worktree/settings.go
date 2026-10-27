// Package worktree sets up the git worktrees of Flock's sites: it fills a new worktree with what
// git doesn't track (dependencies, .env), copy-on-write where the filesystem can, rewrites its
// .env for its own URL, runs the installs and the project's hooks, and removes it again.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project files with the `worktrees:` settings: ConfigFile is committed, LocalFile is this
// machine's and stays out of git.
const (
	ConfigFile = "flock.yml"
	LocalFile  = "flock.local.yml"
)

// DefaultPath is where `flock worktree add` puts a worktree, relative to the site's folder.
const DefaultPath = "../{{folder}}-{{branch}}"

// Settings are the `worktrees:` section of a project's flock.yml and flock.local.yml.
type Settings struct {
	Auto     bool     // set up new worktrees without being asked
	Path     string   // where `flock worktree add` puts them (placeholders, relative to the site)
	Database string   // shared, copy or fresh
	Migrate  bool     // run the migrations for a fresh database
	Copy     []string // untracked paths copied from the site, relative to it
	// Env are values written into the worktree's .env (placeholders allowed), in EnvKeys order.
	Env          map[string]string
	EnvKeys      []string
	AfterCreate  []string
	BeforeDelete []string
}

// Defaults are the settings without any file.
func Defaults() Settings {
	return Settings{Auto: true, Path: DefaultPath, Database: "shared", Migrate: true, Env: map[string]string{}}
}

type file struct {
	Worktrees struct {
		Auto     *bool     `yaml:"auto"`
		Path     *string   `yaml:"path"`
		Database *string   `yaml:"database"`
		Migrate  *bool     `yaml:"migrate"`
		Copy     []string  `yaml:"copy"`
		Env      yaml.Node `yaml:"env"`
		Hooks    struct {
			AfterCreate  []string `yaml:"after_create"`
			BeforeDelete []string `yaml:"before_delete"`
		} `yaml:"hooks"`
	} `yaml:"worktrees"`
}

// Load reads the settings for the worktree at worktree of the site at site. Later files win: the
// worktree's flock.yml (it comes with the branch), the site's flock.local.yml (a new worktree has
// none of its own yet), then the worktree's flock.local.yml. Lists are added up, env values merged
// key by key. With worktree "" (one about to be created) the site's flock.yml is read instead.
func Load(site, worktree string) (Settings, error) {
	files := []string{filepath.Join(worktree, ConfigFile), filepath.Join(site, LocalFile), filepath.Join(worktree, LocalFile)}
	if worktree == "" {
		files = []string{filepath.Join(site, ConfigFile), filepath.Join(site, LocalFile)}
	}
	s := Defaults()
	for _, path := range files {
		if err := s.apply(path); err != nil {
			return s, err
		}
	}
	return s, nil
}

func (s *Settings) apply(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	w := f.Worktrees
	if w.Auto != nil {
		s.Auto = *w.Auto
	}
	if w.Path != nil && *w.Path != "" {
		s.Path = *w.Path
	}
	if w.Database != nil {
		switch *w.Database {
		case "shared", "copy", "fresh":
			s.Database = *w.Database
		default:
			return fmt.Errorf("%s: worktrees.database is shared, copy or fresh, not %q", path, *w.Database)
		}
	}
	if w.Migrate != nil {
		s.Migrate = *w.Migrate
	}
	for _, p := range w.Copy {
		if !slices.Contains(s.Copy, p) {
			s.Copy = append(s.Copy, p)
		}
	}
	s.AfterCreate = append(s.AfterCreate, w.Hooks.AfterCreate...)
	s.BeforeDelete = append(s.BeforeDelete, w.Hooks.BeforeDelete...)

	if w.Env.Kind == yaml.MappingNode {
		// Values as written: `DEBUG: true` and `PORT: 8080` are text in a .env file anyway.
		for i := 0; i+1 < len(w.Env.Content); i += 2 {
			key, value := w.Env.Content[i].Value, w.Env.Content[i+1]
			if value.Kind != yaml.ScalarNode {
				return fmt.Errorf("%s: worktrees.env.%s must be a single value", path, key)
			}
			if !validEnvKey(key) || strings.ContainsAny(value.Value, "\r\n") {
				return fmt.Errorf("%s: worktrees.env.%s can't be written to .env", path, key)
			}
			if _, seen := s.Env[key]; !seen {
				s.EnvKeys = append(s.EnvKeys, key)
			}
			s.Env[key] = value.Value
		}
	} else if w.Env.Kind != 0 {
		return fmt.Errorf("%s: worktrees.env must map names to values", path)
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r != '_' && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// Vars are the values of the placeholders in paths, env values and hooks.
type Vars map[string]string

// Expand fills in {{name}} placeholders; unknown ones stay as they are.
func (v Vars) Expand(s string) string {
	for name, value := range v {
		s = strings.ReplaceAll(s, "{{"+name+"}}", value)
	}
	return s
}
