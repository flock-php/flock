package phpbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// WindowsBuild is an official non-thread-safe x64 build of PHP from windows.php.net.
type WindowsBuild struct {
	Version string // "8.4.13"
	URL     string
	SHA256  string // empty for archived builds, which windows.php.net publishes without checksums
	VS      string // compiler the build (and its extensions) must match, e.g. "vs17"
}

var (
	ntsKey      = regexp.MustCompile(`^nts-(v[sc]\d+)-x64$`)
	archiveName = regexp.MustCompile(`php-(\d+\.\d+\.\d+)-nts-Win32-(v[sc]\d+)-x64\.zip`)
)

// WindowsBuilds returns the newest Windows build of every minor version: current releases from
// releases.json, older (end-of-life) branches from the archive listing.
func (s Sources) WindowsBuilds(ctx context.Context) (map[string]WindowsBuild, error) {
	base := strings.TrimRight(s.WindowsPHP, "/")

	body, err := s.get(ctx, base+"/releases.json")
	if err != nil {
		return nil, err
	}
	builds, err := parseWindowsReleases(body, base)
	if err != nil {
		return nil, err
	}

	// The archive is a convenience for old branches; without it, current releases still work.
	if listing, err := s.get(ctx, base+"/archives/"); err == nil {
		for minor, b := range parseWindowsArchive(string(listing), base+"/archives/") {
			if Compare(b.Version, builds[minor].Version) > 0 {
				builds[minor] = b
			}
		}
	}
	return builds, nil
}

// parseWindowsReleases reads releases.json:
// {"8.4": {"version": "8.4.13", "nts-vs17-x64": {"zip": {"path": "php-...zip", "sha256": "..."}}, ...}, ...}
func parseWindowsReleases(body []byte, base string) (map[string]WindowsBuild, error) {
	var releases map[string]map[string]json.RawMessage
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("windows.php.net releases.json: %w", err)
	}

	builds := map[string]WindowsBuild{}
	for minor, fields := range releases {
		var version string
		if json.Unmarshal(fields["version"], &version) != nil || MinorOf(version) != minor {
			continue
		}
		for key, raw := range fields {
			m := ntsKey.FindStringSubmatch(key)
			if m == nil {
				continue
			}
			var variant struct {
				Zip struct {
					Path   string `json:"path"`
					SHA256 string `json:"sha256"`
				} `json:"zip"`
			}
			if json.Unmarshal(raw, &variant) != nil || variant.Zip.Path == "" {
				continue
			}
			builds[minor] = WindowsBuild{Version: version, URL: base + "/" + variant.Zip.Path, SHA256: strings.ToLower(variant.Zip.SHA256), VS: m[1]}
		}
	}
	return builds, nil
}

// parseWindowsArchive finds the newest archived NTS x64 zip of every minor in a directory listing.
func parseWindowsArchive(listing, base string) map[string]WindowsBuild {
	builds := map[string]WindowsBuild{}
	for _, m := range archiveName.FindAllStringSubmatch(listing, -1) {
		minor := MinorOf(m[1])
		if minor == "" || Compare(minor, MinimumMinor) < 0 {
			continue
		}
		if Compare(m[1], builds[minor].Version) > 0 {
			builds[minor] = WindowsBuild{Version: m[1], URL: base + m[0], VS: m[2]}
		}
	}
	return builds
}

// XdebugDLL finds the newest Xdebug DLL for a PHP minor and compiler on xdebug.org's download page.
// File names changed order over the years: php_xdebug-3.4.5-8.4-nts-vs17-x86_64.dll and
// php_xdebug-3.2.2-8.2-vs16-nts-x86_64.dll.
func (s Sources) XdebugDLL(ctx context.Context, minor, vs string) (string, error) {
	base := strings.TrimRight(s.Xdebug, "/")
	page, err := s.get(ctx, base+"/download")
	if err != nil {
		return "", err
	}
	return findXdebugDLL(string(page), base, minor, vs)
}

func findXdebugDLL(page, base, minor, vs string) (string, error) {
	pattern := regexp.MustCompile(`php_xdebug-(\d+\.\d+\.\d+)-` + regexp.QuoteMeta(minor) +
		`-(?:nts-` + vs + `|` + vs + `-nts)-x86_64\.dll`)

	best, name := "", ""
	for _, m := range pattern.FindAllStringSubmatch(page, -1) {
		if Compare(m[1], best) > 0 {
			best, name = m[1], m[0]
		}
	}
	if name == "" {
		return "", fmt.Errorf("xdebug.org has no Xdebug build for PHP %s (%s, non-thread-safe, x64) yet", minor, vs)
	}
	return base + "/files/" + name, nil
}
