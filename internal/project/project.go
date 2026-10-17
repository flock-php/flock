// Package project reads and writes the PHP and Node.js versions a project pins, and decides which
// version a site or directory uses:
//
//  1. isolated: set for the site in Flock (`flock isolate`), on this machine only;
//  2. project: pinned in the project (`flock pin`), committed with it: .flockrc for PHP and Node,
//     and for Node also .nvmrc / .node-version, which .flockrc wins over;
//  3. default: Flock's default version.
//
// Project files are looked up from the directory upwards, like nvm does with .nvmrc.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/php"
)

// RC is Flock's own project file: "php=8.4" and "node=22" lines, # comments.
const RC = ".flockrc"

// Tools a version can be isolated for or pinned.
const (
	PHP  = "php"
	Node = "node"
)

// Where a version comes from.
const (
	Isolated = "isolated"
	Project  = "project"
	Default  = "default"
)

// Version is the version a site or directory uses, and why.
type Version struct {
	Version string
	Source  string // Isolated, Project or Default
	// Pinned is what the project files say ("" when nothing is pinned), even when an isolation
	// wins over it; File is the file that says it.
	Pinned string
	File   string
	// Shadowed is a Node version file that disagrees with .flockrc's node= (which wins),
	// e.g. ".nvmrc: 20"; empty otherwise.
	Shadowed string
}

// PHPFor decides the PHP version for a site (nil outside sites) in dir.
func PHPFor(cfg *config.Config, site *config.Site, dir string) Version {
	v := Version{Version: cfg.DefaultPHP, Source: Default}
	if pinned, file := findKey(dir, PHP); pinned != "" {
		v.Version, v.Source, v.Pinned, v.File = pinned, Project, pinned, file
	}
	if site != nil && site.PHP != "" {
		v.Version, v.Source = site.PHP, Isolated
	}
	return v
}

// NodeFor decides the Node.js version (a spec, usually a major) for a site (nil outside sites) in dir.
func NodeFor(cfg *config.Config, site *config.Site, dir string) Version {
	v := Version{Version: cfg.DefaultNode, Source: Default}
	spec, specFile := node.DetectSpecFile(dir)
	rc, rcFile := findKey(dir, Node)
	switch {
	case rc != "":
		v.Version, v.Source, v.Pinned, v.File = rc, Project, rc, rcFile
		if spec != "" && spec != rc {
			v.Shadowed = fmt.Sprintf("%s: %s", filepath.Base(specFile), spec)
		}
	case spec != "":
		v.Version, v.Source, v.Pinned, v.File = spec, Project, spec, specFile
	}
	if site != nil && site.Node != "" {
		v.Version, v.Source = site.Node, Isolated
	}
	return v
}

// ErrInvalid marks a wrong tool or version.
var ErrInvalid = errors.New("invalid version")

// ValidVersion checks a version for a tool: a PHP minor ("8.4") or a Node.js major ("22").
func ValidVersion(tool, version string) error {
	switch tool {
	case PHP:
		if php.MinorOf(version) != version {
			return fmt.Errorf("%w: give a PHP version like 8.4, not %q", ErrInvalid, version)
		}
	case Node:
		if node.MajorOf(version) != version {
			return fmt.Errorf("%w: give a Node.js major version like 22, not %q", ErrInvalid, version)
		}
	default:
		return fmt.Errorf("%w: unknown tool %q (php or node)", ErrInvalid, tool)
	}
	return nil
}

// PinSite pins tool's version into the site's project (by default the version the site uses now,
// for Node its major) and lifts the site's isolation for tool, so the project decides from now on.
// It returns the file it wrote and whether it changed site (which the caller then stores).
func PinSite(cfg *config.Config, site *config.Site, tool, version string) (file string, lifted bool, err error) {
	if version == "" {
		switch tool {
		case PHP:
			version = PHPFor(cfg, site, site.Path).Version
		case Node:
			version = node.MajorOf(NodeFor(cfg, site, site.Path).Version)
		}
	}
	if file, err = Pin(site.Path, tool, version); err != nil {
		return "", false, err
	}
	switch {
	case tool == PHP && site.PHP != "":
		site.PHP, lifted = "", true
	case tool == Node && site.Node != "":
		site.Node, lifted = "", true
	}
	return file, lifted, nil
}

// Pin writes version for tool into the project in dir and returns the file it wrote. PHP goes
// into .flockrc; Node into the file the project already names its Node version in (.flockrc's
// node=, .node-version or .nvmrc), else a new .nvmrc, the file other Node tools read too.
func Pin(dir, tool, version string) (string, error) {
	if err := ValidVersion(tool, version); err != nil {
		return "", err
	}
	rc := filepath.Join(dir, RC)
	if tool == PHP {
		return rc, setKey(rc, PHP, version)
	}
	if v, _ := readKey(rc, Node); v != "" {
		return rc, setKey(rc, Node, version)
	}
	file := filepath.Join(dir, ".nvmrc")
	for _, name := range []string{".node-version", ".nvmrc"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			file = filepath.Join(dir, name)
			break
		}
	}
	return file, os.WriteFile(file, []byte(version+"\n"), 0o644)
}

// Unpin removes tool's version from the project in dir: the key from .flockrc (and the file when
// nothing else is left in it), and for Node the .nvmrc / .node-version files. It returns the files
// it changed; none means nothing was pinned in dir itself.
func Unpin(dir, tool string) ([]string, error) {
	var changed []string
	rc := filepath.Join(dir, RC)
	if v, _ := readKey(rc, tool); v != "" {
		if err := deleteKey(rc, tool); err != nil {
			return changed, err
		}
		changed = append(changed, rc)
	}
	if tool == Node {
		for _, name := range node.SpecFiles {
			file := filepath.Join(dir, name)
			if err := os.Remove(file); err == nil {
				changed = append(changed, file)
			} else if !errors.Is(err, os.ErrNotExist) {
				return changed, err
			}
		}
	}
	return changed, nil
}

// Files are the paths whose changes can change the versions of the project in dir (for watching):
// the project files in dir and every parent.
func Files(dir string) []string {
	var files []string
	dir = filepath.Clean(dir)
	for {
		files = append(files, filepath.Join(dir, RC))
		for _, name := range node.SpecFiles {
			files = append(files, filepath.Join(dir, name))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return files
		}
		dir = parent
	}
}

// findKey walks up from dir to the nearest .flockrc that sets key.
func findKey(dir, key string) (value, file string) {
	dir = filepath.Clean(dir)
	for {
		path := filepath.Join(dir, RC)
		if v, err := readKey(path, key); err == nil && v != "" {
			return v, path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}

func readKey(path, key string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := parseLine(line); ok && k == key {
			if key == Node {
				return node.ValidSpec(v), nil
			}
			return php.MinorOf(v), nil // "8.4.1" pins 8.4
		}
	}
	return "", nil
}

func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	k, v, ok := strings.Cut(line, "=")
	return strings.ToLower(strings.TrimSpace(k)), strings.Trim(strings.TrimSpace(v), `"'`), ok
}

// setKey sets key=value in the file, keeping its other lines and comments.
func setKey(path, key, value string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	}
	replaced := false
	for i, line := range lines {
		if k, _, ok := parseLine(line); ok && k == key {
			if replaced {
				lines[i] = "\x00" // a duplicate: drop it
				continue
			}
			lines[i] = key + "=" + value
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, key+"="+value)
	}
	return writeLines(path, lines)
}

// deleteKey removes key from the file; a file left without settings is deleted.
func deleteKey(path, key string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"), "\n")
	settings := 0
	for i, line := range lines {
		if k, _, ok := parseLine(line); ok {
			if k == key {
				lines[i] = "\x00"
				continue
			}
			settings++
		}
	}
	if settings == 0 {
		return os.Remove(path)
	}
	return writeLines(path, lines)
}

func writeLines(path string, lines []string) error {
	var kept []string
	for _, line := range lines {
		if line != "\x00" {
			kept = append(kept, line)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0o644)
}
