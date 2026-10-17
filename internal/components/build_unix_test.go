//go:build !windows

package components

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeUpstream serves a "source tarball" and its published hashes.
func fakeUpstream(t *testing.T, tarball []byte, hash string) Sources {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/hashes":
			fmt.Fprintf(w, "hash valkey-9.1.2.tar.gz sha256 %s https://example/9.1.2.tar.gz\n", hash)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return Sources{
		ValkeyHashes: srv.URL + "/hashes",
		ValkeySource: srv.URL + "/valkey/%s.tar.gz",
		Client:       srv.Client(),
	}
}

func fakeBuildScripts(t *testing.T) {
	t.Helper()
	oldValkey, oldCLT := valkeyScript, requireCLT
	t.Cleanup(func() { valkeyScript, requireCLT = oldValkey, oldCLT })
	requireCLT = func() error { return nil }
	// Pretend to build: check the arguments, "compile" the tarball's contents into the output.
	script := []byte(`set -eu
echo "==> building $2"
test -f "$1"
mkdir -p "$3/bin"
cp "$1" "$3/bin/tool"
`)
	valkeyScript = script
}

func TestBuildVerifiesSourceAndRunsScript(t *testing.T) {
	fakeBuildScripts(t)
	tarball := []byte("source code")
	sum := sha256.Sum256(tarball)
	src := fakeUpstream(t, tarball, hex.EncodeToString(sum[:]))
	ws := t.TempDir()

	var log bytes.Buffer
	var phases []string
	r := NewReporter(&log, func(phase string, _ float64) { phases = append(phases, phase) })
	dir, cleanup, err := BuildValkey(context.Background(), src, ws, "9.1.2", r)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "bin", "tool")); string(got) != "source code" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(log.String(), "==> building 9.1.2") || phases[len(phases)-1] != PhaseBuilding {
		t.Fatalf("log %q phases %v", log.String(), phases)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup left the work dir")
	}
}

func TestBuildRejectsChecksumMismatchAndSpaces(t *testing.T) {
	fakeBuildScripts(t)
	src := fakeUpstream(t, []byte("tampered"), strings.Repeat("ab", 32))
	ws := t.TempDir()
	if _, _, err := BuildValkey(context.Background(), src, ws, "9.1.2", NewReporter(nil, nil)); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if _, _, err := BuildValkey(context.Background(), src, ws, "9.9.9", NewReporter(nil, nil)); err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Fatalf("want missing checksum error, got %v", err)
	}
	if _, _, err := BuildValkey(context.Background(), src, filepath.Join(ws, "with space"), "9.1.2", NewReporter(nil, nil)); err == nil || !strings.Contains(err.Error(), "spaces") {
		t.Fatalf("want spaces error, got %v", err)
	}
}
