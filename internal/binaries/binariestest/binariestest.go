// Package binariestest publishes a signed binaries manifest for tests: packages are added with
// Publish, and every change gets a higher serial, as the release workflow does.
package binariestest

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flock/internal/binaries"
)

// Server serves the stable manifest and the packages it lists.
type Server struct {
	URL  string
	srv  *httptest.Server
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey

	mu       sync.Mutex
	manifest binaries.Manifest
	files    map[string][]byte
}

// New starts a server with an empty manifest; it stops when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	s := &Server{priv: priv, pub: pub, files: map[string][]byte{}, manifest: binaries.Manifest{
		Schema: 1, Channel: binaries.Stable, Serial: 1, Expires: time.Now().Add(time.Hour).Truncate(time.Second),
		Packages: map[string]map[string]map[string]binaries.Entry{},
	}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	s.URL = s.srv.URL
	return s
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.Marshal(s.manifest)
	switch {
	case r.URL.Path == "/manifest.json":
		_, _ = w.Write(data)
	case r.URL.Path == "/manifest.json.sig":
		fmt.Fprintln(w, base64.StdEncoding.EncodeToString(ed25519.Sign(s.priv, data)))
	case strings.HasPrefix(r.URL.Path, "/files/") && s.files[strings.TrimPrefix(r.URL.Path, "/files/")] != nil:
		_, _ = w.Write(s.files[strings.TrimPrefix(r.URL.Path, "/files/")])
	default:
		http.NotFound(w, r)
	}
}

// Source reads the manifest from this server, caching it in cache.
func (s *Server) Source(cache string) binaries.Source {
	return binaries.Source{Bases: []string{s.URL + "/"}, Key: s.pub, HTTP: s.srv.Client(), Cache: cache}
}

// Catalog follows the stable channel of this server.
func (s *Server) Catalog(cache string) *binaries.Catalog {
	return binaries.NewCatalog(s.Source(cache), func() binaries.Channel { return binaries.Stable })
}

// Publish lists a build of pkg's version for this platform, with archive as its .zip. yanked
// lists withdrawn builds (build number → reason).
func (s *Server) Publish(pkg, version string, build int, archive []byte, yanked map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := fmt.Sprintf("%s-%s-build%d-%s.zip", pkg, version, build, binaries.Platform())
	sum := sha256.Sum256(archive)
	s.files[name] = archive
	if s.manifest.Packages[pkg] == nil {
		s.manifest.Packages[pkg] = map[string]map[string]binaries.Entry{}
	}
	s.manifest.Packages[pkg][version] = map[string]binaries.Entry{binaries.Platform(): {
		Build: build, URL: s.URL + "/files/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(archive)), Yanked: yanked,
	}}
	s.manifest.Serial++
}

// Withdraw removes a version of pkg from the manifest.
func (s *Server) Withdraw(pkg, version string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.manifest.Packages[pkg], version)
	s.manifest.Serial++
}

// Zip builds a .zip of files (name → content), in a stable order so the same files give the same bytes.
func Zip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(files[name]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
