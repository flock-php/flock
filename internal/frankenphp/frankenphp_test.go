package frankenphp

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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"flock/internal/binaries"
)

// fakeProbe makes every package look like FrankenPHP 1.13.0 with the PHP version in its "binary".
func fakeProbe(t *testing.T) {
	t.Helper()
	saved := probe
	probe = func(binary string) (Info, error) {
		data, err := os.ReadFile(binary)
		if err != nil {
			return Info{}, err
		}
		return Info{Version: "1.13.0", PHP: strings.TrimSpace(string(data))}, nil
	}
	t.Cleanup(func() { probe = saved })
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "frankenphp.exe"
	}
	return "bin/frankenphp"
}

// packageZip is a package as the release workflow publishes it, with a top-level directory.
func packageZip(t *testing.T, phpVersion string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("frankenphp/" + binaryName())
	_, _ = w.Write([]byte(phpVersion))
	w, _ = zw.Create("frankenphp/ext/php_intl.dll")
	_, _ = w.Write([]byte("dll"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCaddyfileClassicAndOctane(t *testing.T) {
	root := t.TempDir()
	classic := Caddyfile(root, Site{Name: "app", Root: "/srv/app/public", Port: 21000})
	for _, want := range []string{"admin off", "http://:21000 {", "bind 127.0.0.1", `root * "/srv/app/public"`, "php_server {"} {
		if !strings.Contains(classic, want) {
			t.Errorf("classic config lacks %q:\n%s", want, classic)
		}
	}
	if strings.Contains(classic, "worker") || strings.Contains(classic, "HTTPS") {
		t.Errorf("classic config has a worker or HTTPS:\n%s", classic)
	}

	octane := Caddyfile(root, Site{Name: "app", Root: "/srv/app/public", Port: 21001, Secured: true, Octane: true})
	for _, want := range []string{"file " + quote(filepath.Join("/srv/app/public", "frankenphp-worker.php")), "env HTTPS on", "try_files {path} frankenphp-worker.php", "index frankenphp-worker.php"} {
		if !strings.Contains(octane, want) {
			t.Errorf("Octane config lacks %q:\n%s", want, octane)
		}
	}
}

func TestSpecNeedsOctaneAndWritesTheWorker(t *testing.T) {
	fakeProbe(t)
	root, project := t.TempDir(), t.TempDir()
	public := filepath.Join(project, "public")
	_ = os.MkdirAll(public, 0o755)

	src := filepath.Join(t.TempDir(), "pkg")
	_ = os.MkdirAll(filepath.Join(src, filepath.Dir(binaryName())), 0o755)
	_ = os.WriteFile(filepath.Join(src, binaryName()), []byte("8.4.14"), 0o755)
	if minor, info, err := InstallFrom(root, src, 0, nil); err != nil || minor != "8.4" || info.PHP != "8.4.14" {
		t.Fatalf("InstallFrom: %s %+v %v", minor, info, err)
	}

	site := Site{Name: "app", Path: project, Root: public, Minor: "8.4", Port: 21000, Octane: true}
	if _, err := Spec(root, site); err == nil || !strings.Contains(err.Error(), "composer require laravel/octane") {
		t.Fatalf("without Octane: %v", err)
	}

	_ = os.MkdirAll(filepath.Dir(OctaneWorker(project)), 0o755)
	_ = os.WriteFile(OctaneWorker(project), []byte("<?php"), 0o644)
	spec, err := Spec(root, site)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(public, "frankenphp-worker.php")); err != nil {
		t.Error("frankenphp-worker.php wasn't created")
	}
	env := strings.Join(spec.Env, "\n")
	for _, want := range []string{"LARAVEL_OCTANE=1", "APP_BASE_PATH=" + project, "PHP_INI_SCAN_DIR="} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q: %v", want, spec.Env)
		}
	}
	if spec.Name != "frankenphp-app" || spec.Dir != project || spec.Args[0] != "run" {
		t.Errorf("spec: %+v", spec)
	}

	site.Restart++
	again, _ := Spec(root, site)
	if strings.Join(again.Env, "\n") == env {
		t.Error("a restart doesn't change the spec")
	}
}

func TestManagerInstallsAndUpdatesFromTheManifest(t *testing.T) {
	if binaries.Platform() == "" {
		t.Skip("no packages for this platform")
	}
	fakeProbe(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pkg := packageZip(t, "8.4.14")
	sum := sha256.Sum256(pkg)
	var mu sync.Mutex
	build, serial, yanked := 1, int64(1), map[string]string(nil)
	expires := time.Now().Add(time.Hour).Truncate(time.Second)

	var srv *httptest.Server
	manifest := func() []byte {
		mu.Lock()
		defer mu.Unlock()
		platform := binaries.Platform()
		m := binaries.Manifest{Schema: 1, Channel: binaries.Stable, Serial: serial, Expires: expires, Packages: map[string]map[string]map[string]binaries.Entry{
			Package: {"8.4.14": {platform: {Build: build, URL: fmt.Sprintf("%s/frankenphp-8.4.14-build%d-%s.zip", srv.URL, build, platform),
				SHA256: hex.EncodeToString(sum[:]), Size: int64(len(pkg)), Yanked: yanked}}},
		}}
		data, _ := json.Marshal(m)
		return data
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manifest.json":
			_, _ = w.Write(manifest())
		case r.URL.Path == "/manifest.json.sig":
			fmt.Fprintln(w, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest())))
		case strings.HasSuffix(r.URL.Path, ".zip"):
			_, _ = w.Write(pkg)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	root := t.TempDir()
	source := binaries.Source{Bases: []string{srv.URL + "/"}, Key: pub, HTTP: srv.Client(), Cache: filepath.Join(root, "cache")}
	m := NewManager(root, binaries.NewCatalog(source, func() binaries.Channel { return binaries.Stable }))
	installed := make(chan string, 4)
	m.Installed = func(minor string) { installed <- minor }

	if err := m.Install("8.1"); err == nil {
		t.Error("PHP 8.1 accepted")
	}
	m.Ensure("8.4")
	m.Wait()
	if j := m.Job("8.4"); j != nil {
		t.Fatalf("install failed: %+v", j)
	}
	if got := <-installed; got != "8.4" {
		t.Fatalf("installed %s", got)
	}
	info, ok := InstalledInfo(root, "8.4")
	if !ok || info.Version != "1.13.0" || info.PHP != "8.4.14" || info.Build != 1 {
		t.Fatalf("installed info: %+v %v", info, ok)
	}
	st := m.State()
	if len(st.Builds) != 1 || st.Builds[0].Installed != "1.13.0" || st.Builds[0].Latest != "8.4.14" || st.Builds[0].Update || st.Builds[0].Job != nil {
		t.Fatalf("state: %+v", st)
	}

	// A higher build of the same PHP release is an update.
	mu.Lock()
	build, serial = 2, 2
	mu.Unlock()
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := m.State(); !st.Builds[0].Update {
		t.Fatalf("a newer build isn't an update: %+v", st.Builds[0])
	}
	if err := m.Install("8.4"); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	<-installed
	if info, _ := InstalledInfo(root, "8.4"); info.Build != 2 {
		t.Errorf("after the update: %+v", info)
	}

	// Build 2 is withdrawn and build 3 published: it replaces build 2 without being asked.
	mu.Lock()
	build, serial, yanked = 3, 3, map[string]string{"2": "crashes on start"}
	mu.Unlock()
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-installed:
	case <-time.After(10 * time.Second):
		t.Fatal("the withdrawn build wasn't replaced")
	}
	m.Wait()
	if info, _ := InstalledInfo(root, "8.4"); info.Build != 3 {
		t.Errorf("after replacing the withdrawn build: %+v", info)
	}
	if b := m.State().Builds[0]; b.Yanked != "" || b.Update {
		t.Errorf("state after the replacement: %+v", b)
	}

	if runtime.GOOS == "windows" {
		ini, err := os.ReadFile(filepath.Join(Dir(root, "8.4"), "conf.d", "00-flock.ini"))
		if err != nil || !strings.Contains(string(ini), "extension = intl") {
			t.Errorf("extensions ini: %s %v", ini, err)
		}
	}
}
