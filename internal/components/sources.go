package components

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Sources are where the macOS builds look up versions and download sources. Tests point them at
// local servers.
type Sources struct {
	ValkeyReleases string // GitHub API: releases of valkey-io/valkey
	ValkeyHashes   string // valkey-io/valkey-hashes README: "hash <file> sha256 <hex> <url>" lines
	ValkeySource   string // tarball URL, %s = version
	PostgresIndex  string // postgresql.org versions.json
	PostgresSource string // tarball URL, %s = version (twice: directory and file)
	Client         *http.Client
}

// DefaultSources are the upstream locations.
func DefaultSources() Sources {
	return Sources{
		ValkeyReleases: "https://api.github.com/repos/valkey-io/valkey/releases?per_page=100",
		ValkeyHashes:   "https://raw.githubusercontent.com/valkey-io/valkey-hashes/main/README",
		ValkeySource:   "https://github.com/valkey-io/valkey/archive/refs/tags/%s.tar.gz",
		PostgresIndex:  "https://www.postgresql.org/versions.json",
		PostgresSource: "https://ftp.postgresql.org/pub/source/v%[1]s/postgresql-%[1]s.tar.bz2",
		Client:         http.DefaultClient,
	}
}

// ClientMajor is the PostgreSQL major of the client tools: the newest, which work with every
// server version Flock offers.
const ClientMajor = "18"

var stableVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// LatestValkey returns the newest stable Valkey release on GitHub.
func (s Sources) LatestValkey(ctx context.Context) (string, error) {
	data, err := s.get(ctx, s.ValkeyReleases)
	if err != nil {
		return "", err
	}
	return ParseGitHubReleases(data)
}

// ParseGitHubReleases returns the newest stable version among GitHub releases (drafts, pre-releases
// and tags like "9.2.0-rc1" are skipped). Valkey patches older lines too, so the newest version
// is not necessarily the most recent release.
func ParseGitHubReleases(data []byte) (string, error) {
	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return "", fmt.Errorf("parse GitHub releases: %w", err)
	}
	newest := ""
	for _, r := range releases {
		v := strings.TrimPrefix(r.Tag, "v")
		if r.Draft || r.Prerelease || !stableVersion.MatchString(v) {
			continue
		}
		if newest == "" || Newer(v, newest) {
			newest = v
		}
	}
	if newest == "" {
		return "", errors.New("no stable Valkey release found on GitHub")
	}
	return newest, nil
}

// ValkeySHA256 returns the published SHA-256 of the source tarball of version.
func (s Sources) ValkeySHA256(ctx context.Context, version string) (string, error) {
	data, err := s.get(ctx, s.ValkeyHashes)
	if err != nil {
		return "", err
	}
	return ParseValkeyHashes(data, version)
}

// ParseValkeyHashes finds the sha256 of valkey-<version>.tar.gz in valkey-hashes' README.
func ParseValkeyHashes(data []byte, version string) (string, error) {
	want := "valkey-" + version + ".tar.gz"
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "hash" && f[1] == want && f[2] == "sha256" && len(f[3]) == 64 {
			return strings.ToLower(f[3]), nil
		}
	}
	return "", fmt.Errorf("valkey-hashes has no checksum for Valkey %s yet — try again later", version)
}

// LatestPostgres returns the newest release of the PostgreSQL major ("18.6").
func (s Sources) LatestPostgres(ctx context.Context, major string) (string, error) {
	data, err := s.get(ctx, s.PostgresIndex)
	if err != nil {
		return "", err
	}
	return ParsePostgresVersions(data, major)
}

// ParsePostgresVersions finds the newest release of major in postgresql.org's versions.json.
func ParsePostgresVersions(data []byte, major string) (string, error) {
	var versions []struct {
		Major       string `json:"major"`
		LatestMinor string `json:"latestMinor"`
	}
	if err := json.Unmarshal(data, &versions); err != nil {
		return "", fmt.Errorf("parse versions.json: %w", err)
	}
	for _, v := range versions {
		if v.Major == major && v.LatestMinor != "" {
			return v.Major + "." + v.LatestMinor, nil
		}
	}
	return "", fmt.Errorf("postgresql.org lists no PostgreSQL %s release", major)
}

// PostgresSourceURL is the source tarball of a PostgreSQL release.
func (s Sources) PostgresSourceURL(version string) string {
	return fmt.Sprintf(s.PostgresSource, version)
}

// PostgresSHA256 reads the .sha256 file published next to the tarball.
func (s Sources) PostgresSHA256(ctx context.Context, version string) (string, error) {
	data, err := s.get(ctx, s.PostgresSourceURL(version)+".sha256")
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(data))
	if len(f) == 0 || len(f[0]) != 64 {
		return "", fmt.Errorf("unexpected checksum file for PostgreSQL %s: %q", version, strings.TrimSpace(string(data)))
	}
	return strings.ToLower(f[0]), nil
}

func (s Sources) get(ctx context.Context, url string) ([]byte, error) {
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden && strings.Contains(url, "api.github.com") {
		return nil, errors.New("GitHub's limit for anonymous requests (60 per hour) was reached — try again in an hour")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
