package postgres

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Majors are the PostgreSQL versions Flock offers, newest first.
var Majors = []string{"18", "17", "16", "15", "14"}

// DefaultMajor is the major `flock postgres install` installs without arguments.
const DefaultMajor = "18"

// EndOfLife is when each major stops receiving fixes upstream (postgresql.org/support/versioning).
var EndOfLife = map[string]string{
	"14": "2026-11-12",
	"15": "2027-11-11",
	"16": "2028-11-09",
	"17": "2029-11-08",
	"18": "2030-11-14",
}

// CheckInterval is how old the catalog may get before the daemon fetches it again.
var CheckInterval = 24 * time.Hour

var fullVersion = regexp.MustCompile(`^(\d+)\.\d+\.\d+$`)

// MajorOf returns the major of a zonky version ("18.6.0" → "18"), or "".
func MajorOf(version string) string {
	if m := fullVersion.FindStringSubmatch(version); m != nil {
		return m[1]
	}
	return ""
}

// Newer reports whether dotted version a is newer than b ("18.10.0" > "18.9.0").
func Newer(a, b string) bool {
	return compareVersions(a, b) > 0
}

func compareVersions(a, b string) int {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(x), len(y)); i++ {
		var p, q int
		if i < len(x) {
			p, _ = strconv.Atoi(x[i])
		}
		if i < len(y) {
			q, _ = strconv.Atoi(y[i])
		}
		if p != q {
			if p > q {
				return 1
			}
			return -1
		}
	}
	return 0
}

// MetadataURL is the Maven metadata listing every zonky version for platform.
func MetadataURL(baseURL, platform string) string {
	return fmt.Sprintf("%s/embedded-postgres-binaries-%s/maven-metadata.xml", strings.TrimRight(baseURL, "/"), platform)
}

// ParseMetadata returns the newest version of every major in Majors listed in a maven-metadata.xml.
func ParseMetadata(data []byte) (map[string]string, error) {
	var meta struct {
		Versions []string `xml:"versioning>versions>version"`
	}
	if err := xml.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse maven-metadata.xml: %w", err)
	}
	latest := map[string]string{}
	for _, v := range meta.Versions {
		v = strings.TrimSpace(v)
		major := MajorOf(v)
		if !slices.Contains(Majors, major) {
			continue
		}
		if cur, ok := latest[major]; !ok || Newer(v, cur) {
			latest[major] = v
		}
	}
	if len(latest) == 0 {
		return nil, errors.New("maven-metadata.xml lists no PostgreSQL 14–18 versions")
	}
	return latest, nil
}

// Catalog caches the newest version of every major for one platform in a file, so the daemon
// answers at once and remembers what it knew across restarts.
type Catalog struct {
	Path   string // cache file
	URL    string // maven-metadata.xml
	Client *http.Client

	mu       sync.Mutex
	loaded   bool
	state    catalogState
	checkMu  sync.Mutex
	checking atomic.Bool
}

type catalogState struct {
	CheckedAt  time.Time         `json:"checked_at"`
	CheckError string            `json:"check_error"`
	Latest     map[string]string `json:"latest"` // major → version, e.g. "18" → "18.6.0"
}

// NewCatalog creates the catalog of the current platform, cached under root.
func NewCatalog(root string) (*Catalog, error) {
	inst, err := NewInstaller(root)
	if err != nil {
		return nil, err
	}
	return &Catalog{
		Path:   filepath.Join(root, "cache", "postgres-versions.json"),
		URL:    MetadataURL(inst.BaseURL, inst.Platform),
		Client: http.DefaultClient,
	}, nil
}

// load must be called with c.mu held.
func (c *Catalog) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	if data, err := os.ReadFile(c.Path); err == nil {
		_ = json.Unmarshal(data, &c.state)
	}
}

// Latest returns the newest version per major, when they were fetched and the last check's error.
func (c *Catalog) Latest() (map[string]string, time.Time, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	out := map[string]string{}
	for k, v := range c.state.Latest {
		out[k] = v
	}
	return out, c.state.CheckedAt, c.state.CheckError
}

// Stale reports whether the catalog was never fetched or is older than CheckInterval.
func (c *Catalog) Stale() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	return c.state.CheckedAt.IsZero() || time.Since(c.state.CheckedAt) > CheckInterval
}

// CheckInBackground starts a check unless one is running.
func (c *Catalog) CheckInBackground() {
	if !c.checking.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.checking.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = c.Check(ctx)
	}()
}

// Check fetches the metadata now. A failed check keeps the versions known before.
func (c *Catalog) Check(ctx context.Context) error {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	latest, err := c.fetch(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	c.state.CheckedAt = time.Now().UTC()
	if err != nil {
		c.state.CheckError = err.Error()
	} else {
		c.state.CheckError, c.state.Latest = "", latest
	}
	if data, merr := json.MarshalIndent(c.state, "", "  "); merr == nil {
		_ = os.MkdirAll(filepath.Dir(c.Path), 0o755)
		_ = os.WriteFile(c.Path, data, 0o644)
	}
	return err
}

func (c *Catalog) fetch(ctx context.Context) (map[string]string, error) {
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", c.URL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return ParseMetadata(data)
}

// Run keeps the catalog fresh until ctx ends: it checks soon after start when stale, then
// whenever it is older than CheckInterval (looked at hourly, so a sleeping Mac catches up).
func (c *Catalog) Run(ctx context.Context) {
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if c.Stale() {
				checkCtx, cancel := context.WithTimeout(ctx, time.Minute)
				_ = c.Check(checkCtx)
				cancel()
			}
			timer.Reset(time.Hour)
		}
	}
}
