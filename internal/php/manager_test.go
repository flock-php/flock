package php

import (
	"archive/zip"
	"bytes"
	"context"
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
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flock/internal/binaries"
)

// fakeProbe makes every php binary report the version written into it.
func fakeProbe(t *testing.T) {
	t.Helper()
	saved := probe
	probe = func(binary string) (string, error) {
		data, err := os.ReadFile(binary)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	t.Cleanup(func() { probe = saved })
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(files[name]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// phpPackage is a PHP package as the release workflow publishes it for this platform.
func phpPackage(t *testing.T, version string) []byte {
	if runtime.GOOS == "windows" {
		return zipOf(t, map[string]string{"php.exe": version, "php-cgi.exe": "cgi", "php8ts.dll": "dll", "ext/php_intl.dll": "dll"})
	}
	return zipOf(t, map[string]string{"bin/php": version, "bin/php-fpm": "fpm", "ext/imagick.so": "so", "ext/pcov.so": "so", "ext/xdebug.so": "so"})
}

func TestManagerInstallsUpdatesAndReplacesWithdrawnBuilds(t *testing.T) {
	if binaries.Platform() == "" {
		t.Skip("no packages for this platform")
	}
	fakeProbe(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	windows := runtime.GOOS == "windows"
	pack := zipOf(t, map[string]string{"ext/php_imagick.dll": "dll", "ext/php_pcov.dll": "dll", "CORE_RL_MagickCore_.dll": "lib", "LICENSES/imagick.txt": "license"})

	var mu sync.Mutex
	version, build, extsBuild, serial, yanked := "8.4.25", 1, 1, int64(1), map[string]string(nil)
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	var srv *httptest.Server
	packages := func() map[string][]byte {
		mu.Lock()
		defer mu.Unlock()
		return map[string][]byte{"php": phpPackage(t, version), "exts": pack}
	}
	manifest := func() []byte {
		files := packages()
		mu.Lock()
		defer mu.Unlock()
		platform := binaries.Platform()
		entry := func(name string, build int, yanked map[string]string) binaries.Entry {
			sum := sha256.Sum256(files[name])
			return binaries.Entry{Build: build, URL: fmt.Sprintf("%s/%s-%d.zip", srv.URL, name, build), SHA256: hex.EncodeToString(sum[:]), Size: int64(len(files[name])), Yanked: yanked}
		}
		m := binaries.Manifest{Schema: 1, Channel: binaries.Stable, Serial: serial, Expires: expires, Packages: map[string]map[string]map[string]binaries.Entry{
			Package: {version: {platform: entry("php", build, yanked)}, "8.3.30": {platform: entry("php", 1, nil)}},
		}}
		if windows {
			m.Packages[ExtensionsPackage] = map[string]map[string]binaries.Entry{"8.4": {platform: entry("exts", extsBuild, nil)}}
		}
		data, _ := json.Marshal(m)
		return data
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manifest.json":
			_, _ = w.Write(manifest())
		case r.URL.Path == "/manifest.json.sig":
			fmt.Fprintln(w, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest())))
		case strings.HasPrefix(r.URL.Path, "/php-"):
			_, _ = w.Write(packages()["php"])
		case strings.HasPrefix(r.URL.Path, "/exts-"):
			_, _ = w.Write(pack)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	source := binaries.Source{Bases: []string{srv.URL + "/"}, Key: pub, HTTP: srv.Client(), Cache: filepath.Join(root, "cache")}
	m := NewManager(root, binaries.NewCatalog(source, func() binaries.Channel { return binaries.Stable }))
	finished := make(chan string, 8)
	m.Finished = func(minor string) { finished <- minor }
	install := func() {
		t.Helper()
		if err := m.Install("8.4"); err != nil {
			t.Fatal(err)
		}
		m.Wait()
		<-finished
		if j := m.Job("8.4"); j != nil {
			t.Fatalf("install failed: %+v", j)
		}
	}

	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rs := m.Releases(); len(rs) != 2 || rs[0].Minor != "8.4" || rs[0].Latest != "8.4.25" || rs[0].Status != "available" || rs[1].Minor != "8.3" {
		t.Fatalf("releases before installing: %+v", rs)
	}

	install()
	info, ok := InstalledInfo(root, "8.4")
	if !ok || info.Version != "8.4.25" || info.Build != 1 || (windows && info.ExtensionsBuild != 1) {
		t.Fatalf("installed: %+v %v", info, ok)
	}
	if r := m.Releases()[0]; r.Installed != "8.4.25" || r.Status != "installed" || r.Update {
		t.Fatalf("after installing: %+v", r)
	}

	// The extensions load everywhere, pcov only for the CLI, Xdebug only for debugging.
	ini, _ := os.ReadFile(filepath.Join(Dir(root, "8.4"), "conf.d", "00-flock.ini"))
	if !strings.Contains(string(ini), "extension = imagick") || strings.Contains(string(ini), "pcov") || strings.Contains(string(ini), "xdebug") {
		t.Errorf("extensions ini:\n%s", ini)
	}
	if windows && !strings.Contains(string(ini), "extension = intl") {
		t.Errorf("the official build's extensions aren't loaded:\n%s", ini)
	}
	cli, _ := os.ReadFile(filepath.Join(CLIDir(root, "8.4"), "pcov.ini"))
	if !strings.Contains(string(cli), ExtensionPath(root, "8.4", "pcov")) || !strings.Contains(string(cli), "pcov.enabled = 1") {
		t.Errorf("pcov ini:\n%s", cli)
	}
	if scan := strings.Join(CLIEnv(root, "8.4"), " "); !strings.Contains(scan, CLIDir(root, "8.4")) {
		t.Errorf("the CLI doesn't scan %s: %s", CLIDir(root, "8.4"), scan)
	}
	if scan := strings.Join(Env(root, "8.4"), " "); strings.Contains(scan, CLIDir(root, "8.4")) {
		t.Errorf("the pools scan %s: %s", CLIDir(root, "8.4"), scan)
	}
	if windows {
		if _, err := os.Stat(filepath.Join(Dir(root, "8.4"), "bin", "CORE_RL_MagickCore_.dll")); err != nil {
			t.Errorf("ImageMagick isn't next to php.exe: %v", err)
		}
	}

	// php.ini is the user's: an update keeps it.
	if err := os.WriteFile(IniPath(root, "8.4"), []byte("memory_limit = 1G\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A newer patch is an update.
	mu.Lock()
	version, serial = "8.4.26", 2
	mu.Unlock()
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := m.Releases()[0]; !r.Update || r.Status != "update" || r.Latest != "8.4.26" {
		t.Fatalf("a newer patch isn't an update: %+v", r)
	}
	install()
	if info, _ := InstalledInfo(root, "8.4"); info.Version != "8.4.26" {
		t.Errorf("after the update: %+v", info)
	}
	if data, _ := os.ReadFile(IniPath(root, "8.4")); string(data) != "memory_limit = 1G\n" {
		t.Errorf("php.ini was replaced: %q", data)
	}

	// On Windows a rebuilt extension pack is an update too.
	if windows {
		mu.Lock()
		extsBuild, serial = 2, 3
		mu.Unlock()
		if err := m.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r := m.Releases()[0]; !r.Update {
			t.Fatalf("a newer extension pack isn't an update: %+v", r)
		}
		install()
	}

	// Build 1 is withdrawn and build 2 published: it replaces build 1 without being asked.
	mu.Lock()
	build, serial, yanked = 2, 4, map[string]string{"1": "crashes on start"}
	mu.Unlock()
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("the withdrawn build wasn't replaced")
	}
	m.Wait()
	if info, _ := InstalledInfo(root, "8.4"); info.Build != 2 {
		t.Errorf("after replacing the withdrawn build: %+v", info)
	}
	if r := m.Releases()[0]; r.Yanked != "" || r.Update {
		t.Errorf("after the replacement: %+v", r)
	}
}

func TestInstallFromAPackage(t *testing.T) {
	fakeProbe(t)
	dir := filepath.Join(t.TempDir(), "package")
	files := map[string]string{"bin/php": "8.5.11", "bin/php-fpm": "fpm", "ext/mongodb.so": "so"}
	if runtime.GOOS == "windows" {
		files = map[string]string{"php.exe": "8.5.11", "php-cgi.exe": "cgi", "ext/php_mongodb.dll": "dll"}
	}
	for name, body := range files {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	root := t.TempDir()
	minor, err := InstallFrom(root, dir)
	if err != nil || minor != "8.5" {
		t.Fatalf("got %q %v", minor, err)
	}
	if info, ok := InstalledInfo(root, "8.5"); !ok || info.Version != "8.5.11" || info.Build != 0 {
		t.Fatalf("installed: %+v %v", info, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "ext")); err != nil {
		t.Fatal("the source directory must be left alone")
	}
	if ini, _ := os.ReadFile(filepath.Join(Dir(root, "8.5"), "conf.d", "00-flock.ini")); !strings.Contains(string(ini), "extension = mongodb") {
		t.Errorf("extensions ini:\n%s", ini)
	}
}

func TestJobText(t *testing.T) {
	for _, c := range []struct {
		job  Job
		want string
	}{
		{Job{Phase: "downloading", Progress: 0.42}, "Downloading PHP 8.4… 42%"},
		{Job{Phase: "downloading", Progress: -1}, "Downloading PHP 8.4…"},
		{Job{Phase: "installing", Progress: -1}, "Installing PHP 8.4…"},
	} {
		if got := c.job.Text("8.4"); got != c.want {
			t.Errorf("%+v: %q, want %q", c.job, got, c.want)
		}
	}
	if MinorOf("8.6.0RC1") != "8.6" || MinorOf("garbage") != "" || MinorOf("8") != "" {
		t.Error("MinorOf")
	}
}
