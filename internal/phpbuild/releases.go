// Package phpbuild finds out which PHP versions exist, which of them static-php-cli can build,
// and builds them in the background so installing or updating is a single click.
package phpbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MinimumMinor is the oldest PHP version Flock lists; MinimumBuildable the oldest that still
// compiles: PHP 8.0 and 7.4 fail against current libxml2 (fixed upstream only from 8.1).
const (
	MinimumMinor     = "7.4"
	MinimumBuildable = "8.1"
)

// Release is the newest release of one PHP minor version.
type Release struct {
	Minor     string `json:"minor"`
	Latest    string `json:"latest"`
	Supported bool   `json:"supported"` // the branch still gets bug or security fixes upstream
}

// Sources are the URLs version information is read from (overridable in tests).
type Sources struct {
	PHPNet     string // https://www.php.net
	StaticPHP  string // directory listing of static-php-cli's own builds
	WindowsPHP string // windows.php.net's releases directory (Windows builds)
	Xdebug     string // https://xdebug.org (Xdebug DLLs for Windows)
	HTTP       *http.Client
}

// DefaultSources point at php.net, static-php.dev, windows.php.net and xdebug.org.
func DefaultSources() Sources {
	return Sources{
		PHPNet:     "https://www.php.net",
		StaticPHP:  "https://dl.static-php.dev/static-php-cli/bulk/",
		WindowsPHP: "https://windows.php.net/downloads/releases",
		Xdebug:     "https://xdebug.org",
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}
}

// Releases returns the latest release of every minor from MinimumMinor up, newest first.
func (s Sources) Releases(ctx context.Context) ([]Release, error) {
	latest := map[string]string{}
	for _, major := range []int{7, 8, 9} {
		url := fmt.Sprintf("%s/releases/index.php?json&version=%d&max=1000", s.PHPNet, major)
		body, err := s.get(ctx, url)
		if err != nil {
			return nil, err
		}
		// A major without releases yet (9) answers with an array or an error object, not a version map.
		var releases map[string]json.RawMessage
		if json.Unmarshal(body, &releases) != nil {
			continue
		}
		for version := range releases {
			minor := MinorOf(version)
			if minor == "" || Compare(minor, MinimumMinor) < 0 {
				continue
			}
			if Compare(version, latest[minor]) > 0 {
				latest[minor] = version
			}
		}
	}

	var active map[string]map[string]json.RawMessage
	if err := s.getJSON(ctx, s.PHPNet+"/releases/active.php", &active); err != nil {
		return nil, err
	}
	supported := map[string]bool{}
	for _, branches := range active {
		for minor := range branches {
			supported[minor] = true
		}
	}

	out := make([]Release, 0, len(latest))
	for minor, version := range latest {
		out = append(out, Release{Minor: minor, Latest: version, Supported: supported[minor]})
	}
	sort.Slice(out, func(i, j int) bool { return Compare(out[i].Minor, out[j].Minor) > 0 })
	return out, nil
}

// StaticPHPMinors returns the minors static-php-cli has published macOS FPM builds for on this
// architecture — proof that the tooling can build them (it lags php.net for new minors).
func (s Sources) StaticPHPMinors(ctx context.Context) (map[string]bool, error) {
	body, err := s.get(ctx, s.StaticPHP)
	if err != nil {
		return nil, err
	}
	arch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[runtime.GOARCH]
	pattern := regexp.MustCompile(`php-(\d+\.\d+)\.\d+-fpm-macos-` + regexp.QuoteMeta(arch))

	minors := map[string]bool{}
	for _, m := range pattern.FindAllStringSubmatch(string(body), -1) {
		minors[m[1]] = true
	}
	return minors, nil
}

func (s Sources) getJSON(ctx context.Context, url string, out any) error {
	body, err := s.get(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s: %w", url, err)
	}
	return nil
}

func (s Sources) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// MinorOf turns "8.5.11" into "8.5" ("" if it is not a version).
func MinorOf(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return ""
	}
	for _, p := range parts[:2] {
		if _, err := strconv.Atoi(p); err != nil {
			return ""
		}
	}
	return parts[0] + "." + parts[1]
}

// Compare compares dotted numeric versions ("" sorts first); suffixes like "RC1" are ignored.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		na, nb := part(pa, i), part(pb, i)
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
	}
	return 0
}

func part(parts []string, i int) int {
	if i >= len(parts) {
		return -1
	}
	digits := parts[i]
	if end := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
		digits = digits[:end]
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return -1
	}
	return n
}
