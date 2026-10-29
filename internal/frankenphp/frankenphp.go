// Package frankenphp installs FrankenPHP builds and runs sites on them.
//
// FrankenPHP is a PHP app server: Caddy with PHP embedded. Each build embeds one PHP minor, so
// Flock keeps one per minor:
//
//	frankenphp/<minor>/                 the package as published (bin/frankenphp and ext/ on macOS;
//	                                    frankenphp.exe next to the official PHP TS build on Windows)
//	frankenphp/<minor>/flock.json       what was installed (FrankenPHP and PHP version, build)
//	frankenphp/<minor>/conf.d/          Flock's ini files (extensions on Windows)
//	frankenphp/sites/<site>/Caddyfile   a site's server config, regenerated on every start
//
// A site on FrankenPHP gets a process of its own on a loopback port; Flock's web server keeps
// its domains and certificates and proxies to it. php.ini is the one of the regular PHP of the
// same minor, so settings apply to both.
package frankenphp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"flock/internal/archive"
	"flock/internal/php"
)

// MinimumMinor is the oldest PHP version FrankenPHP supports.
const MinimumMinor = "8.2"

// Package is FrankenPHP's name in the binaries manifest.
const Package = "frankenphp"

var minorPattern = regexp.MustCompile(`^\d+\.\d+$`)

// Supports reports whether FrankenPHP runs PHP minor.
func Supports(minor string) bool {
	return minorPattern.MatchString(minor) && compare(minor, MinimumMinor) >= 0
}

// Dir is where the build for a PHP minor lives.
func Dir(root, minor string) string { return filepath.Join(root, "frankenphp", minor) }

// Binary is the frankenphp executable of an installed build ("" when there is none).
func Binary(root, minor string) string { return findBinary(Dir(root, minor)) }

func findBinary(dir string) string {
	name := "frankenphp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, path := range []string{filepath.Join(dir, "bin", name), filepath.Join(dir, name)} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// Info describes an installed build.
type Info struct {
	Version string `json:"version"` // FrankenPHP, e.g. "1.13.0"
	PHP     string `json:"php"`     // the embedded PHP, e.g. "8.4.14"
	Build   int    `json:"build"`   // the package build from the manifest (0 when installed from a file)
	// ExtensionsBuild is the build of the extension pack added on Windows (0 without one).
	ExtensionsBuild int `json:"extensions_build,omitempty"`
}

func infoFile(root, minor string) string { return filepath.Join(Dir(root, minor), "flock.json") }

// Installed lists the PHP minors that have a build, newest first.
func Installed(root string) []string {
	entries, _ := os.ReadDir(filepath.Join(root, "frankenphp"))
	var minors []string
	for _, e := range entries {
		if e.IsDir() && minorPattern.MatchString(e.Name()) && Binary(root, e.Name()) != "" {
			minors = append(minors, e.Name())
		}
	}
	sort.Slice(minors, func(i, j int) bool { return compare(minors[i], minors[j]) > 0 })
	return minors
}

// InstalledInfo is what is installed for a PHP minor; ok is false when nothing is.
func InstalledInfo(root, minor string) (Info, bool) {
	if Binary(root, minor) == "" {
		return Info{}, false
	}
	var info Info
	if data, err := os.ReadFile(infoFile(root, minor)); err == nil {
		_ = json.Unmarshal(data, &info)
	}
	return info, true
}

// ErrBlocked is returned when Windows refuses to run a build: Smart App Control (or an App Control
// policy) only runs signed programs or ones with a good reputation.
var ErrBlocked = errors.New("Windows blocked FrankenPHP: Smart App Control (or an App Control policy) only runs signed programs")

// statusIntegrityViolation is the exit status of a process Windows' code integrity refused to load.
const statusIntegrityViolation = 0xC0E90002

var versionLine = regexp.MustCompile(`FrankenPHP v?(\d[^\s]*) PHP (\d+\.\d+\.\d+)`)

// probe runs `frankenphp version` (replaced in tests): "FrankenPHP v1.13.0 PHP 8.4.14 Caddy v2.10.2 h1:...".
var probe = func(binary string) (Info, error) {
	out, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && uint32(exit.ExitCode()) == statusIntegrityViolation {
			return Info{}, ErrBlocked
		}
		return Info{}, fmt.Errorf("run %s version: %v: %s", filepath.Base(binary), err, strings.TrimSpace(string(out)))
	}
	m := versionLine.FindStringSubmatch(string(out))
	if m == nil {
		return Info{}, fmt.Errorf("unexpected `frankenphp version` output: %q", strings.TrimSpace(string(out)))
	}
	return Info{Version: m[1], PHP: m[2]}, nil
}

// InstallFrom installs a FrankenPHP package (a directory, .tar.gz or .zip) as the build for the
// PHP minor it embeds, replacing the one there. build is the manifest's build number (0 for a
// local file). release, when set, is called before the old build is replaced, so whatever runs it
// can be stopped (Windows locks the files of running programs). It returns what was installed.
func InstallFrom(root, src string, build int, release func(minor string)) (string, Info, error) {
	base := filepath.Join(root, "frankenphp")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", Info{}, err
	}
	staging, err := os.MkdirTemp(base, ".install-")
	if err != nil {
		return "", Info{}, err
	}
	defer os.RemoveAll(staging)

	dir := filepath.Join(staging, "build")
	if info, err := os.Stat(src); err == nil && info.IsDir() {
		err = copyTree(src, dir)
	} else if archive.IsArchive(src) {
		err = archive.Extract(src, dir)
	} else if err == nil {
		err = fmt.Errorf("%s is not a directory, .tar.gz or .zip", src)
	}
	if err != nil {
		return "", Info{}, err
	}
	return activate(root, dir, build, 0, release)
}

// activate moves an unpacked package into place as the build of the minor it embeds.
// extensionsBuild is the build of the extension pack merged into it (0 for none).
func activate(root, dir string, build, extensionsBuild int, release func(minor string)) (string, Info, error) {
	binary := findBinary(dir)
	if binary == "" {
		return "", Info{}, errors.New("the package has no frankenphp executable (bin/frankenphp or frankenphp.exe)")
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(binary, 0o755)
	}
	info, err := probe(binary)
	if err != nil {
		return "", Info{}, err
	}
	info.Build, info.ExtensionsBuild = build, extensionsBuild
	minor := minorOf(info.PHP)
	if !Supports(minor) {
		return "", Info{}, fmt.Errorf("the package embeds PHP %s; FrankenPHP needs %s or newer", info.PHP, MinimumMinor)
	}
	data, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "flock.json"), append(data, '\n'), 0o644); err != nil {
		return "", Info{}, err
	}

	if release != nil {
		release(minor)
	}
	target := Dir(root, minor)
	old := target + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, old); err != nil {
			return "", Info{}, fmt.Errorf("replace FrankenPHP for PHP %s: %w", minor, err)
		}
	}
	if err := os.Rename(dir, target); err != nil {
		_ = os.Rename(old, target)
		return "", Info{}, err
	}
	_ = os.RemoveAll(old)
	if err := EnsureIni(root, minor); err != nil {
		return "", Info{}, err
	}
	return minor, info, nil
}

// Uninstall removes the build for a PHP minor.
func Uninstall(root, minor string) error {
	if !minorPattern.MatchString(minor) {
		return fmt.Errorf("invalid PHP version %q", minor)
	}
	return os.RemoveAll(Dir(root, minor))
}

// confDir holds Flock's ini files for a build.
func confDir(root, minor string) string { return filepath.Join(Dir(root, minor), "conf.d") }

// EnsureIni (re)writes Flock's ini files for a build: the extensions in its ext/ load as for the
// regular PHP (on Windows the official build's plus Flock's extension pack; on macOS the larger
// ones the build doesn't compile in), and Flock's defaults apply as for it (see php.DefaultsIni).
func EnsureIni(root, minor string) error {
	dir := confDir(root, minor)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := php.ExtensionsIni(filepath.Join(Dir(root, minor), "ext")) + php.DefaultsIni(root, minor)
	return os.WriteFile(filepath.Join(dir, "00-flock.ini"), []byte(body), 0o644)
}

// Env is the environment a build runs with: the regular PHP's php.ini (when that version is
// installed) and Flock's scan dir, so a build never reads another PHP's config.
func Env(root, minor string) []string {
	env := []string{"PHP_INI_SCAN_DIR=" + confDir(root, minor)}
	if ini := php.IniPath(root, minor); fileExists(ini) {
		env = append(env, "PHPRC="+ini)
	}
	if runtime.GOOS == "windows" {
		for _, cnf := range []string{
			filepath.Join(Dir(root, minor), "extras", "ssl", "openssl.cnf"),
			filepath.Join(php.Dir(root, minor), "bin", "openssl.cnf"),
		} {
			if fileExists(cnf) {
				env = append(env, "OPENSSL_CONF="+cnf)
				break
			}
		}
	}
	return env
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm()|0o600)
	})
}

// minorOf turns "8.4.14" into "8.4".
func minorOf(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// compare compares dotted numeric versions.
func compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
