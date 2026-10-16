// Package binaries reads the signed manifest of Flock's prebuilt packages (github.com/flock-php/binaries)
// and downloads packages from it.
//
// The manifest lists, for every package, its versions and for each version one entry per platform.
// It is signed as a whole with ed25519; Flock rejects a manifest whose signature doesn't verify,
// that has expired, or whose serial is lower than the last one it accepted (an old manifest can't
// be replayed to hold back updates).
package binaries

import (
	"context"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"flock/internal/archive"
	"flock/internal/download"
)

// DefaultURL is where the release workflow publishes the manifest; its signature is at DefaultURL + ".sig".
const DefaultURL = "https://github.com/flock-php/binaries/releases/download/manifest/manifest.json"

//go:embed manifest.pub
var publicKey string

// PublicKey is the key the manifest is signed with.
func PublicKey() ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(publicKey))
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("binaries: invalid embedded manifest.pub")
	}
	return key
}

// Manifest is every published package.
type Manifest struct {
	Schema    int                                    `json:"schema"`
	Serial    int64                                  `json:"serial"`
	Generated time.Time                              `json:"generated"`
	Expires   time.Time                              `json:"expires"`
	Packages  map[string]map[string]map[string]Entry `json:"packages"` // package → version → platform
}

// Entry is the newest build of a package version for one platform. A higher Build of the same
// version replaces an installed one.
type Entry struct {
	Build     int       `json:"build"`
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Published time.Time `json:"published"`
}

// Platform names this machine in the manifest ("macos-arm64", "windows-x64"); "" where Flock
// publishes no packages.
func Platform() string {
	return platform(runtime.GOOS, runtime.GOARCH)
}

func platform(goos, goarch string) string {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "macos-arm64"
	case goos == "windows" && goarch == "amd64":
		return "windows-x64"
	}
	return ""
}

// Source fetches and verifies the manifest.
type Source struct {
	URL  string
	Key  ed25519.PublicKey
	HTTP *http.Client
	// SerialFile remembers the highest serial accepted, so an older manifest is refused.
	SerialFile string
}

// DefaultSource reads the published manifest, remembering its serial under root.
func DefaultSource(root string) Source {
	return Source{
		URL:        DefaultURL,
		Key:        PublicKey(),
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		SerialFile: filepath.Join(root, "cache", "manifest-serial"),
	}
}

// Fetch downloads the manifest and its signature and verifies both.
func (s Source) Fetch(ctx context.Context) (*Manifest, error) {
	data, err := s.get(ctx, s.URL)
	if err != nil {
		return nil, err
	}
	sig, err := s.get(ctx, s.URL+".sig")
	if err != nil {
		return nil, err
	}
	return s.verify(data, sig, time.Now())
}

func (s Source) verify(data, sig []byte, now time.Time) (*Manifest, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize || !ed25519.Verify(s.Key, data, raw) {
		return nil, errors.New("the package manifest's signature doesn't verify")
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse the package manifest: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("the package manifest has schema %d — update Flock", m.Schema)
	}
	if now.After(m.Expires) {
		return nil, fmt.Errorf("the package manifest expired on %s", m.Expires.Format(time.DateOnly))
	}
	if s.SerialFile != "" {
		if last := s.lastSerial(); m.Serial < last {
			return nil, fmt.Errorf("the package manifest is older than one already seen (serial %d < %d)", m.Serial, last)
		}
		_ = os.MkdirAll(filepath.Dir(s.SerialFile), 0o755)
		_ = os.WriteFile(s.SerialFile, []byte(strconv.FormatInt(m.Serial, 10)+"\n"), 0o644)
	}
	return &m, nil
}

func (s Source) lastSerial() int64 {
	data, err := os.ReadFile(s.SerialFile)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return n
}

func (s Source) get(ctx context.Context, url string) ([]byte, error) {
	client := s.HTTP
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Install downloads e into a work directory next to dir, verifies its SHA-256 and unpacks it
// into dir, which must not exist yet. progress gets the bytes downloaded so far and the total.
func Install(ctx context.Context, client *http.Client, e Entry, dir string, progress func(done, total int64)) error {
	work := dir + ".download"
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(work)

	name := filepath.Base(strings.SplitN(e.URL, "?", 2)[0])
	if !archive.IsArchive(name) {
		return fmt.Errorf("%s is not a .zip or .tar.gz", name)
	}
	file := filepath.Join(work, name)
	if e.SHA256 == "" {
		return fmt.Errorf("the package manifest has no checksum for %s", name)
	}
	if err := download.File(ctx, client, e.URL, file, e.SHA256, progress); err != nil {
		return err
	}
	return archive.Extract(file, dir)
}
