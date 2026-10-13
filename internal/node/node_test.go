package node

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fakeIndex = `[
	{"version":"v23.1.0"},
	{"version":"v22.11.0"},
	{"version":"v22.9.0"},
	{"version":"v20.18.0"}
]`

// fakeArchive builds a tiny Node-shaped archive for the current platform.
func fakeArchive(t *testing.T, version string) []byte {
	t.Helper()
	top := strings.TrimSuffix(strings.TrimSuffix(archiveName(version, runtime.GOOS, runtime.GOARCH), ".tar.gz"), ".zip")

	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(top + "/node.exe")
		_, _ = w.Write([]byte("node"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(hdr *tar.Header, body string) {
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(&tar.Header{Name: top + "/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	add(&tar.Header{Name: top + "/bin/node", Typeflag: tar.TypeReg, Mode: 0o755}, "#!/bin/sh\necho "+version)
	add(&tar.Header{Name: top + "/lib/node_modules/npm/bin/npm-cli.js", Typeflag: tar.TypeReg, Mode: 0o755}, "npm")
	add(&tar.Header{Name: top + "/bin/npm", Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npm-cli.js"}, "")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fakeDist(t *testing.T, corruptSum bool) *httptest.Server {
	t.Helper()

	archives := map[string][]byte{}
	for _, v := range []string{"22.11.0", "20.18.0"} {
		archives[v] = fakeArchive(t, v)
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			fmt.Fprint(w, fakeIndex)
			return
		}
		for v, data := range archives {
			name := archiveName(v, runtime.GOOS, runtime.GOARCH)
			switch r.URL.Path {
			case "/v" + v + "/SHASUMS256.txt":
				sum := sha256.Sum256(data)
				if corruptSum {
					sum[0] ^= 0xff
				}
				fmt.Fprintf(w, "%s  other-file.tar.gz\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
				return
			case "/v" + v + "/" + name:
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
	}))
}

func TestInstallResolvesMajorAndVerifiesChecksum(t *testing.T) {
	srv := fakeDist(t, false)
	defer srv.Close()
	root := t.TempDir()
	inst := &Installer{Root: root, BaseURL: srv.URL}

	version, err := inst.Install(context.Background(), "22")
	if err != nil || version != "22.11.0" {
		t.Fatalf("want 22.11.0, got %q, %v", version, err)
	}

	binDir, resolved, err := Resolve(root, "22")
	if err != nil || resolved != "22.11.0" {
		t.Fatalf("resolve: %q, %v", resolved, err)
	}

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(filepath.Join(binDir, "node.exe")); err != nil {
			t.Fatal(err)
		}
		return
	}

	info, err := os.Stat(filepath.Join(binDir, "node"))
	if err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("node must be executable: %v, %v", info, err)
	}
	link, err := os.Readlink(filepath.Join(binDir, "npm"))
	if err != nil || link != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Fatalf("npm symlink not preserved: %q, %v", link, err)
	}
	if _, err := os.Stat(filepath.Join(binDir, "npm")); err != nil {
		t.Fatalf("npm symlink must resolve: %v", err)
	}

	// Installing again is a no-op.
	if again, err := inst.Install(context.Background(), "22.11"); err != nil || again != "22.11.0" {
		t.Fatalf("reinstall: %q, %v", again, err)
	}
}

func TestInstallFailsOnChecksumMismatchAndLeavesNothingBehind(t *testing.T) {
	srv := fakeDist(t, true)
	defer srv.Close()
	root := t.TempDir()

	_, err := (&Installer{Root: root, BaseURL: srv.URL}).Install(context.Background(), "22")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}

	if versions, _ := Installed(root); len(versions) != 0 {
		t.Fatalf("nothing should be installed, got %v", versions)
	}
}

func TestInstallRejectsUnknownSpec(t *testing.T) {
	srv := fakeDist(t, false)
	defer srv.Close()

	if _, err := (&Installer{Root: t.TempDir(), BaseURL: srv.URL}).Install(context.Background(), "99"); err == nil {
		t.Fatal("want error for unknown major")
	}
}

func TestNewestMatch(t *testing.T) {
	versions := []string{"22.9.0", "23.1.0", "22.11.0", "20.18.0"}
	cases := map[string]string{"22": "22.11.0", "22.9": "22.9.0", "20.18.0": "20.18.0", "23": "23.1.0"}

	for spec, want := range cases {
		if got, ok := newestMatch(versions, spec); !ok || got != want {
			t.Errorf("%s: want %s, got %s", spec, want, got)
		}
	}
	if _, ok := newestMatch(versions, "2"); ok {
		t.Error(`"2" must not match 22.x`)
	}
}

func TestResolvePicksNewestInstalled(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"22.9.0", "22.11.0", "20.18.0", ".tmp-22.12.0-x"} {
		if err := os.MkdirAll(filepath.Join(Dir(root), v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	installed, _ := Installed(root)
	if strings.Join(installed, ",") != "22.11.0,22.9.0,20.18.0" {
		t.Fatalf("unexpected installed list: %v", installed)
	}

	if _, v, err := Resolve(root, "22"); err != nil || v != "22.11.0" {
		t.Fatalf("want 22.11.0, got %q, %v", v, err)
	}
	if _, _, err := Resolve(root, "18"); err == nil {
		t.Fatal("want error when nothing matches")
	}
}

func TestDetectSpecWalksUp(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "app", "resources", "js")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := DetectSpec(deep); got != "" {
		t.Fatalf("want empty, got %q", got)
	}

	_ = os.WriteFile(filepath.Join(root, "app", ".node-version"), []byte("v20.18.0\n"), 0o644)
	if got := DetectSpec(deep); got != "20.18.0" {
		t.Fatalf("want 20.18.0, got %q", got)
	}

	_ = os.WriteFile(filepath.Join(root, "app", "resources", ".nvmrc"), []byte(" 22 \n"), 0o644)
	if got := DetectSpec(deep); got != "22" {
		t.Fatalf("want 22, got %q", got)
	}

	_ = os.WriteFile(filepath.Join(deep, ".nvmrc"), []byte("lts/*\n"), 0o644)
	if got := DetectSpec(deep); got != "" {
		t.Fatalf("aliases must map to empty, got %q", got)
	}
}
