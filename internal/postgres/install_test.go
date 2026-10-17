package postgres

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

type entry struct {
	name, body, link string
}

// fakeJar builds a zonky-shaped jar: a zip holding postgres-<platform>.txz.
func fakeJar(t *testing.T, entries []entry) []byte {
	t.Helper()

	var tarBuf bytes.Buffer
	xw, err := xz.NewWriter(&tarBuf)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(xw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if e.link != "" {
			hdr = &tar.Header{Name: e.name, Linkname: e.link, Typeflag: tar.TypeSymlink}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}

	var jar bytes.Buffer
	zw := zip.NewWriter(&jar)
	if _, err := zw.Create("META-INF/MANIFEST.MF"); err != nil {
		t.Fatal(err)
	}
	w, err := zw.Create("postgres-darwin-arm_64.txz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	return jar.Bytes()
}

func serveJar(t *testing.T, jar []byte, sum string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".jar.sha1"):
			_, _ = w.Write([]byte(sum))
		case strings.HasSuffix(r.URL.Path, ".jar"):
			_, _ = w.Write(jar)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func sha1Hex(b []byte) string {
	s := sha1.Sum(b)
	return hex.EncodeToString(s[:])
}

func TestJarURLAndPlatform(t *testing.T) {
	got := JarURL(DefaultBaseURL+"/", "darwin-arm64v8", "18.6.0")
	want := "https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-darwin-arm64v8/18.6.0/embedded-postgres-binaries-darwin-arm64v8-18.6.0.jar"
	if got != want {
		t.Fatalf("got %s", got)
	}

	if p, _ := Platform("windows", "amd64"); p != "windows-amd64" {
		t.Fatalf("got %s", p)
	}
	if _, err := Platform("linux", "riscv64"); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}

func TestInstallVerifiesAndExtractsAtomically(t *testing.T) {
	requireSymlinks(t)
	jar := fakeJar(t, []entry{
		{name: "bin/" + exe("postgres"), body: "#!/bin/sh\n"},
		{name: "lib/libpq.5.dylib", body: "lib"},
		{name: "lib/libpq.dylib", link: "libpq.5.dylib"},
		{name: "share/postgresql/postgres.bki", body: "bki"},
	})
	srv := serveJar(t, jar, sha1Hex(jar)+"\n")

	inst := &Installer{Root: t.TempDir(), BaseURL: srv.URL, Platform: "darwin-arm64v8", Client: srv.Client()}
	if err := inst.Install(context.Background(), "18.6.0"); err != nil {
		t.Fatal(err)
	}

	if !inst.Installed("18.6.0") {
		t.Fatal("expected installed")
	}
	if runtime.GOOS != "windows" {
		if target, err := os.Readlink(filepath.Join(inst.Dir("18.6.0"), "lib", "libpq.dylib")); err != nil || target != "libpq.5.dylib" {
			t.Fatalf("symlink not preserved: %q %v", target, err)
		}
	}

	leftovers, _ := filepath.Glob(filepath.Join(inst.Root, "services", "postgres", ".*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp dirs left behind: %v", leftovers)
	}
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	jar := fakeJar(t, []entry{{name: "bin/" + exe("postgres"), body: "x"}})
	srv := serveJar(t, jar, strings.Repeat("0", 40))

	inst := &Installer{Root: t.TempDir(), BaseURL: srv.URL, Platform: "darwin-arm64v8", Client: srv.Client()}
	err := inst.Install(context.Background(), "18.6.0")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if inst.Installed("18.6.0") {
		t.Fatal("must not install on checksum mismatch")
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	for _, e := range []entry{
		{name: "../evil", body: "x"},
		{name: "lib/evil", link: "../../etc/passwd"},
	} {
		jar := fakeJar(t, []entry{e})
		if err := extractJar(jar, t.TempDir()); err == nil {
			t.Fatalf("expected %q to be rejected", e.name)
		}
	}
}

// requireSymlinks skips a test where symbolic links can't be made: Windows allows them only in
// Developer Mode or for administrators (GitHub's runners are).
func requireSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symbolic links aren't allowed here: %v", err)
	}
}
