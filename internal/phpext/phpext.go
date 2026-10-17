// Package phpext reports which PHP extensions a project needs: the ones it and its packages
// require in composer.json and composer.lock (`ext-*`), the ones its configuration shows it uses
// without requiring them (Laravel only, for now), and whether the site's PHP has each of them.
//
// Requiring every extension in composer.json is what makes `composer install` refuse a server
// without it, instead of the app failing there at runtime. The hints point out the ones a project
// uses but forgot to require.
package phpext

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ComposerJSON is the project's own requirements; a package name stands for the dependency that
// requires an extension.
const ComposerJSON = "composer.json"

// Extension is one extension a project needs.
type Extension struct {
	// Name is Composer's name without "ext-": lower case, spaces as dashes ("intl", "zend-opcache").
	Name string `json:"name"`
	// RequiredBy is who requires it: "composer.json" and/or the packages from composer.lock.
	RequiredBy []string `json:"required_by"`
	// Hint is why the project seems to use it although nothing requires it, e.g.
	// "DB_CONNECTION=pgsql in .env"; "" when something requires it.
	Hint string `json:"hint,omitempty"`
	// Available is whether the site's PHP has it.
	Available bool `json:"available"`
}

// Declared reports whether composer.json or one of the packages requires it.
func (e Extension) Declared() bool { return len(e.RequiredBy) > 0 }

// Report is what a project needs, for the PHP version it uses.
type Report struct {
	PHP        string      `json:"php"`
	Laravel    bool        `json:"laravel"` // hints are looked for (Laravel projects only)
	Extensions []Extension `json:"extensions"`
}

// Missing lists the required extensions the site's PHP doesn't have.
func (r Report) Missing() []string {
	var out []string
	for _, e := range r.Extensions {
		if e.Declared() && !e.Available {
			out = append(out, e.Name)
		}
	}
	return out
}

// Check builds the report for the project in dir. available is what the site's PHP has (see
// Available); nil means nothing is known, and every extension counts as missing.
func Check(dir, phpVersion string, available map[string]bool) Report {
	byName := map[string]*Extension{}
	get := func(name string) *Extension {
		if e, ok := byName[name]; ok {
			return e
		}
		e := &Extension{Name: name, RequiredBy: []string{}}
		byName[name] = e
		return e
	}
	for name, by := range Requirements(dir) {
		get(name).RequiredBy = by
	}
	laravel := IsLaravel(dir)
	if laravel {
		for name, why := range Hints(dir) {
			if e := get(name); !e.Declared() {
				e.Hint = why
			}
		}
	}

	out := Report{PHP: phpVersion, Laravel: laravel, Extensions: []Extension{}}
	for _, e := range byName {
		e.Available = Has(available, e.Name)
		out.Extensions = append(out.Extensions, *e)
	}
	sort.Slice(out.Extensions, func(i, j int) bool { return out.Extensions[i].Name < out.Extensions[j].Name })
	return out
}

// Normalize turns a loaded extension's name ("Zend OPcache", "PDO") or a requirement
// ("ext-pdo_mysql") into Composer's spelling without "ext-".
func Normalize(name string) string {
	name = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "ext-")
	return strings.ReplaceAll(name, " ", "-")
}

// Has reports whether available contains name, knowing Composer's aliases for opcache.
func Has(available map[string]bool, name string) bool {
	name = Normalize(name)
	if available[name] {
		return true
	}
	switch name {
	case "opcache", "zend-opcache":
		return available["opcache"] || available["zend-opcache"]
	}
	return false
}

type composerFile struct {
	Require    map[string]json.RawMessage `json:"require"`
	RequireDev map[string]json.RawMessage `json:"require-dev"`
}

type lockFile struct {
	Packages    []lockPackage `json:"packages"`
	PackagesDev []lockPackage `json:"packages-dev"`
}

type lockPackage struct {
	Name    string            `json:"name"`
	Require map[string]string `json:"require"`
}

// Requirements are the extensions composer.json and the locked packages require, each with who
// requires it ("composer.json" first, then the packages by name).
func Requirements(dir string) map[string][]string {
	out := map[string][]string{}
	add := func(name, by string) {
		if !strings.HasPrefix(strings.ToLower(name), "ext-") {
			return
		}
		name = Normalize(name)
		for _, existing := range out[name] {
			if existing == by {
				return
			}
		}
		out[name] = append(out[name], by)
	}

	var composer composerFile
	if data, err := os.ReadFile(filepath.Join(dir, ComposerJSON)); err == nil && json.Unmarshal(data, &composer) == nil {
		for _, req := range []map[string]json.RawMessage{composer.Require, composer.RequireDev} {
			for name := range req {
				add(name, ComposerJSON)
			}
		}
	}
	var lock lockFile
	if data, err := os.ReadFile(filepath.Join(dir, "composer.lock")); err == nil && json.Unmarshal(data, &lock) == nil {
		for _, p := range append(lock.Packages, lock.PackagesDev...) {
			for name := range p.Require {
				add(name, p.Name)
			}
		}
	}
	for name, by := range out {
		sort.SliceStable(by, func(i, j int) bool { return by[i] == ComposerJSON && by[j] != ComposerJSON })
		out[name] = by
	}
	return out
}

// IsLaravel reports whether dir is a Laravel app: it has artisan and requires laravel/framework.
func IsLaravel(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "artisan")); err != nil {
		return false
	}
	var composer composerFile
	data, err := os.ReadFile(filepath.Join(dir, ComposerJSON))
	if err != nil || json.Unmarshal(data, &composer) != nil {
		return false
	}
	_, ok := composer.Require["laravel/framework"]
	return ok
}

// Hints are the extensions a Laravel app's configuration shows it uses: name → why.
func Hints(dir string) map[string]string {
	env := readEnv(filepath.Join(dir, ".env"))
	out := map[string]string{}
	hint := func(name, why string) {
		if _, ok := out[name]; !ok {
			out[name] = why
		}
	}
	is := func(key string, values ...string) (string, bool) {
		v := strings.ToLower(env[key])
		for _, want := range values {
			if v == want {
				return key + "=" + v + " in .env", true
			}
		}
		return "", false
	}

	// Laravel's database drivers are PDO's.
	if why, ok := is("DB_CONNECTION", "mysql", "mariadb"); ok {
		hint("pdo_mysql", why)
	}
	if why, ok := is("DB_CONNECTION", "pgsql"); ok {
		hint("pdo_pgsql", why)
	}
	if why, ok := is("DB_CONNECTION", "sqlite"); ok {
		hint("pdo_sqlite", why)
	}
	if why, ok := is("DB_CONNECTION", "sqlsrv"); ok {
		hint("pdo_sqlsrv", why)
	}
	if why, ok := is("DB_CONNECTION", "mongodb"); ok {
		hint("mongodb", why)
	}

	for _, key := range []string{"CACHE_STORE", "CACHE_DRIVER", "SESSION_DRIVER"} {
		if why, ok := is(key, "memcached"); ok {
			hint("memcached", why)
		}
		if why, ok := is(key, "apc"); ok {
			hint("apcu", why)
		}
	}

	// Redis goes through phpredis unless REDIS_CLIENT says predis (a package, no extension).
	if client := strings.ToLower(env["REDIS_CLIENT"]); client == "" || client == "phpredis" {
		for _, key := range []string{"CACHE_STORE", "CACHE_DRIVER", "SESSION_DRIVER", "QUEUE_CONNECTION", "BROADCAST_CONNECTION"} {
			if why, ok := is(key, "redis"); ok {
				if client == "phpredis" {
					why += " (REDIS_CLIENT=phpredis)"
				}
				hint("redis", why)
			}
		}
	}

	// Intervention Image's Laravel integration picks its driver in config/image.php.
	if why, ok := is("IMAGE_DRIVER", "imagick"); ok {
		hint("imagick", why)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "config", "image.php")); err == nil && bytes.Contains(data, []byte(`Drivers\Imagick\Driver`)) {
		hint("imagick", "the Imagick driver in config/image.php")
	}
	return out
}

// readEnv reads KEY=value lines of a .env file (comments, `export` and quotes as dotenv has them).
func readEnv(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		out[strings.TrimSpace(key)] = value
	}
	return out
}
