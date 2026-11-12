package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Site types (see Site.Type).
const (
	TypePHP  = "php"  // served by PHP: FastCGI or FrankenPHP (see Site.Server)
	TypeNode = "node" // a Node.js app whose dev server Flock runs
)

// Kind is the site's type, TypePHP for sites stored before types existed.
func (s Site) Kind() string {
	if s.Type == "" {
		return TypePHP
	}
	return s.Type
}

// TypeName is how a site type is shown: "PHP", "Node.js".
func TypeName(t string) string {
	if t == TypeNode {
		return "Node.js"
	}
	return "PHP"
}

// SetType makes site a site of type t. The site becomes linked so the setting can be stored; the
// caller saves it with Upsert. A PHP site's settings (server, document root) are kept for
// switching back.
func (c *Config) SetType(site *Site, t string) error {
	if t != TypePHP && t != TypeNode {
		return fmt.Errorf("unknown site type %q (php or node)", t)
	}
	if site.IsWorktree() {
		return fmt.Errorf("%s is a git worktree of %s and uses its type — change it on %s", site.Name, site.Parent, site.Parent)
	}
	site.Type, site.Parked = t, false
	return nil
}

// phpMarkers are files only a PHP project has at its top.
var phpMarkers = []string{"composer.json", "artisan", "index.php", "public/index.php", "web/index.php", "wp-config.php"}

// DetectType tells what the project in dir is: TypeNode for a Node.js app (a package.json with a
// dev script, and nothing of PHP's), else TypePHP. A PHP app's package.json (Vite's, in Laravel)
// doesn't make it a Node.js app.
func DetectType(dir string) string {
	for _, f := range phpMarkers {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return TypePHP
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return TypePHP
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Scripts["dev"] == "" {
		return TypePHP
	}
	return TypeNode
}
