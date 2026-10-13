package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultScheduleURL is Node.js' release schedule: when each major line starts, becomes LTS,
// enters maintenance and reaches its end of life.
const DefaultScheduleURL = "https://raw.githubusercontent.com/nodejs/Release/main/schedule.json"

// MinimumMajor is the oldest line offered for installing: older LTS lines stay listed (as end of
// life) because Laravel projects pin them, like PHP 7.4.
const MinimumMajor = 16

// Support is where a line is in its life cycle.
const (
	Current     = "current"     // newest line, not LTS (yet)
	LTS         = "lts"         // active long-term support
	Maintenance = "maintenance" // security fixes only
	EndOfLife   = "eol"
)

// Line is one major version of Node.js, e.g. 22.
type Line struct {
	Major     string `json:"major"`
	Latest    string `json:"latest"`      // newest release, e.g. "22.11.0"
	Codename  string `json:"codename"`    // LTS name ("Jod"), empty for non-LTS lines
	Support   string `json:"support"`     // Current, LTS, Maintenance or EndOfLife; empty when unknown
	EndOfLife string `json:"end_of_life"` // date, e.g. "2027-04-30"; empty when unknown
}

// Lines fetches the published releases and the schedule, and returns the lines worth offering,
// newest first: supported ones, and end-of-life LTS lines back to MinimumMajor.
func (i *Installer) Lines(ctx context.Context, now time.Time) ([]Line, error) {
	data, err := i.fetch(ctx, "index.json")
	if err != nil {
		return nil, err
	}
	var index []struct {
		Version string          `json:"version"`
		LTS     json.RawMessage `json:"lts"` // a codename, or false
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("parse Node.js index: %w", err)
	}

	// The schedule only adds support dates; without it the lines are listed without them.
	schedule, _ := i.schedule(ctx)

	latest := map[int]Line{}
	for _, release := range index {
		version := strings.TrimPrefix(release.Version, "v")
		parts, ok := parse(version)
		if !ok || len(parts) != 3 || parts[0] < MinimumMajor {
			continue
		}
		if seen, ok := latest[parts[0]]; ok && !Newer(version, seen.Latest) {
			continue
		}
		var codename string
		_ = json.Unmarshal(release.LTS, &codename)
		latest[parts[0]] = Line{Major: strconv.Itoa(parts[0]), Latest: version, Codename: codename}
	}

	var lines []Line
	for major, line := range latest {
		if s, ok := schedule[fmt.Sprintf("v%d", major)]; ok {
			line.Support, line.EndOfLife = s.support(now), s.End
		}
		// Odd lines are never LTS: once they end there is no reason to install them.
		if line.Support == EndOfLife && major%2 == 1 {
			continue
		}
		lines = append(lines, line)
	}
	sortLines(lines)
	return lines, nil
}

type scheduleEntry struct {
	Start       string `json:"start"`
	LTS         string `json:"lts"`
	Maintenance string `json:"maintenance"`
	End         string `json:"end"`
}

func (s scheduleEntry) support(now time.Time) string {
	reached := func(date string) bool {
		t, err := time.Parse("2006-01-02", date)
		return err == nil && !now.Before(t)
	}
	switch {
	case reached(s.End):
		return EndOfLife
	case reached(s.Maintenance):
		return Maintenance
	case s.LTS != "" && reached(s.LTS):
		return LTS
	default:
		return Current
	}
}

func (i *Installer) schedule(ctx context.Context) (map[string]scheduleEntry, error) {
	url := i.ScheduleURL
	if url == "" {
		url = DefaultScheduleURL
	}
	data, err := i.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var schedule map[string]scheduleEntry
	if err := json.Unmarshal(data, &schedule); err != nil {
		return nil, fmt.Errorf("parse Node.js schedule: %w", err)
	}
	return schedule, nil
}

// Newer reports whether version a is newer than b ("22.11.0" > "22.9.1").
func Newer(a, b string) bool {
	versions := []string{a, b}
	sortDesc(versions)
	return versions[0] == a && a != b
}

func sortLines(lines []Line) {
	slices.SortFunc(lines, func(a, b Line) int { return cmp.Compare(majorOf(b.Major), majorOf(a.Major)) })
}

func majorOf(v string) int {
	parts, _ := parse(v)
	if len(parts) == 0 {
		return -1
	}
	return parts[0]
}

// MajorOf returns the major of a version ("22.11.0" â†’ "22"), or "" when it isn't one.
func MajorOf(version string) string {
	if n := majorOf(version); n >= 0 {
		return strconv.Itoa(n)
	}
	return ""
}

// Uninstall removes every installed version of major, and the global packages installed with it.
func Uninstall(root, major string) error {
	if err := os.RemoveAll(GlobalDir(root, major)); err != nil {
		return err
	}
	versions, err := Installed(root)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if MajorOf(v) == major {
			if err := os.RemoveAll(filepath.Join(Dir(root), v)); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemoveOlder deletes the installed versions of version's major older than it (after an update).
// A version still in use (a running dev server) stays; Resolve picks the newest anyway.
func RemoveOlder(root, version string) {
	versions, _ := Installed(root)
	for _, v := range versions {
		if v != version && MajorOf(v) == MajorOf(version) && Newer(version, v) {
			_ = os.RemoveAll(filepath.Join(Dir(root), v))
		}
	}
}

// Catalog caches the release lines, so the settings pane can ask often.
type Catalog struct {
	Installer *Installer
	MaxAge    time.Duration

	mu        sync.Mutex
	lines     []Line
	checkedAt time.Time
	err       error

	npm          *npmPackument
	npmCheckedAt time.Time
	npmErr       error
}

// Lines returns the cached lines, fetching them when they are older than MaxAge or refresh is set
// (after a failed fetch, again a minute later).
func (c *Catalog) Lines(ctx context.Context, refresh bool) ([]Line, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	age := time.Since(c.checkedAt)
	if refresh || c.checkedAt.IsZero() || age > c.MaxAge || (c.err != nil && age > time.Minute) {
		lines, err := c.Installer.Lines(ctx, time.Now())
		c.checkedAt, c.err = time.Now(), err
		if err == nil {
			c.lines = lines
		}
	}
	return c.lines, c.checkedAt, c.err
}
