package postgres

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeService records what the Manager applies, like config.json would.
type fakeService struct {
	mu      sync.Mutex
	version string
	enabled bool
	running bool
	live    bool
	applied []string
}

func (f *fakeService) Active() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, f.enabled
}

func (f *fakeService) Apply(version string, enable bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = version
	f.enabled = f.enabled || enable
	f.applied = append(f.applied, version)
	return nil
}

func (f *fakeService) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}
func (f *fakeService) Port() int  { return 1 }
func (f *fakeService) Live() bool { return f.live }

// fakeZonky serves a maven-metadata.xml listing versions and a jar (any version) whose initdb
// creates a cluster of the major passed in the environment-free way: it reads it from its path.
func fakeZonky(t *testing.T, versions *atomic.Value) *httptest.Server {
	t.Helper()
	// initdb -D <dir> ...: the major is the name of the data directory.
	initdb := "#!/bin/sh\nmkdir -p \"$2\" && basename \"$2\" > \"$2/PG_VERSION\"\n"
	jar := fakeJar(t, []entry{
		{name: "bin/postgres", body: "#!/bin/sh\n"},
		{name: "bin/initdb", body: initdb},
		{name: "bin/pg_ctl", body: "#!/bin/sh\n"},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "maven-metadata.xml"):
			body := "<metadata><versioning><versions>"
			for _, v := range versions.Load().([]string) {
				body += "<version>" + v + "</version>"
			}
			_, _ = w.Write([]byte(body + "</versions></versioning></metadata>"))
		case strings.HasSuffix(r.URL.Path, ".jar.sha1"):
			_, _ = w.Write([]byte(sha1Hex(jar)))
		case strings.HasSuffix(r.URL.Path, ".jar"):
			_, _ = w.Write(jar)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestManager(t *testing.T, versions ...string) (*Manager, *fakeService, *atomic.Value) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake initdb is a shell script")
	}
	var published atomic.Value
	published.Store(versions)
	srv := fakeZonky(t, &published)
	root := t.TempDir()
	svc := &fakeService{}
	m := &Manager{
		Installer: &Installer{Root: root, BaseURL: srv.URL, Platform: "darwin-arm64v8", Client: srv.Client()},
		Catalog:   &Catalog{Path: filepath.Join(root, "cache", "pg.json"), URL: MetadataURL(srv.URL, "darwin-arm64v8"), Client: srv.Client()},
		Service:   svc,
	}
	return m, svc, &published
}

func major(t *testing.T, v Versions, m string) MajorVersion {
	t.Helper()
	got, ok := v.Find(m)
	if !ok {
		t.Fatalf("major %s missing", m)
	}
	return got
}

func TestManagerLifecycle(t *testing.T) {
	m, svc, published := newTestManager(t, "17.5.0", "17.6.0", "18.6.0")
	ctx := context.Background()

	// Every offered major is listed, newest first, even before anything is installed.
	v := m.Versions()
	if len(v.Majors) != 5 || v.Majors[0].Major != "18" || v.Majors[4].Major != "14" || v.Active != "" {
		t.Fatalf("got %+v", v)
	}

	// The first install becomes active, gets a cluster and the service is switched on.
	if err := m.Install(ctx, "17", ""); err != nil {
		t.Fatal(err)
	}
	if svc.version != "17.6.0" || !svc.enabled {
		t.Fatalf("service: %+v", svc)
	}
	v = m.Versions()
	if v.Active != "17" || !major(t, v, "17").Active || !major(t, v, "17").HasData || major(t, v, "17").Installed != "17.6.0" {
		t.Fatalf("got %+v", v.Majors)
	}
	if major(t, v, "17").DataSize == 0 || major(t, v, "17").EOL != "2029-11-08" {
		t.Fatalf("got %+v", major(t, v, "17"))
	}

	// A second major is installed but doesn't take over.
	if err := m.Install(ctx, "18", ""); err != nil {
		t.Fatal(err)
	}
	if svc.version != "17.6.0" || m.Versions().Active != "17" || major(t, m.Versions(), "18").HasData {
		t.Fatalf("18 must not take over: %+v", svc)
	}

	// Switching creates 18's own cluster and keeps 17's.
	if err := m.Activate(ctx, "18"); err != nil {
		t.Fatal(err)
	}
	v = m.Versions()
	if svc.version != "18.6.0" || v.Active != "18" || !major(t, v, "18").HasData || !major(t, v, "17").HasData {
		t.Fatalf("got %+v", v)
	}

	// A minor update of an inactive major replaces its binaries and keeps its data.
	published.Store([]string{"17.6.0", "17.7.0", "18.6.0"})
	if err := m.Catalog.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !major(t, m.Versions(), "17").Update {
		t.Fatal("17 should have an update")
	}
	if err := m.Update(ctx, "17"); err != nil {
		t.Fatal(err)
	}
	if m.Installer.Installed("17.6.0") || !m.Installer.Installed("17.7.0") || svc.version != "18.6.0" {
		t.Fatalf("update: %+v", svc)
	}
	if v := major(t, m.Versions(), "17"); v.Update || v.Installed != "17.7.0" || !v.HasData {
		t.Fatalf("got %+v", v)
	}

	// Uninstalling the active major hands over to the other one, starts it (even when the
	// service was switched off) and keeps the data.
	svc.mu.Lock()
	svc.enabled = false
	svc.mu.Unlock()
	if err := m.Uninstall(ctx, "18", false); err != nil {
		t.Fatal(err)
	}
	v = m.Versions()
	if svc.version != "17.7.0" || !svc.enabled || v.Active != "17" || major(t, v, "18").Installed != "" || !major(t, v, "18").HasData {
		t.Fatalf("got %+v / %+v", svc, v.Majors)
	}

	// With --delete-data the cluster goes too, and nothing is active any more.
	if err := m.Uninstall(ctx, "17", true); err != nil {
		t.Fatal(err)
	}
	v = m.Versions()
	if svc.version != "" || v.Active != "" || major(t, v, "17").HasData {
		t.Fatalf("got %+v / %+v", svc, v.Majors)
	}
	// 18's data, kept above, can still be deleted on its own.
	if err := m.Uninstall(ctx, "18", true); err != nil || major(t, m.Versions(), "18").HasData {
		t.Fatalf("delete leftover data: %v", err)
	}
}

func TestManagerInstallExactVersionAndUpdateThroughInstall(t *testing.T) {
	m, svc, _ := newTestManager(t, "18.4.0", "18.6.0")
	ctx := context.Background()

	if err := m.Install(ctx, "18", "18.4.0"); err != nil {
		t.Fatal(err)
	}
	if svc.version != "18.4.0" {
		t.Fatalf("got %s", svc.version)
	}
	// Installing the same or an older release does nothing.
	if err := m.Install(ctx, "18", "18.4.0"); err != nil || len(svc.applied) != 1 {
		t.Fatalf("reinstall: %v %v", err, svc.applied)
	}
	// Installing the newest release of an installed major is an update.
	if err := m.Install(ctx, "18", ""); err != nil {
		t.Fatal(err)
	}
	if svc.version != "18.6.0" || m.Installer.Installed("18.4.0") {
		t.Fatalf("got %s", svc.version)
	}
	if err := m.Install(ctx, "18", "17.6.0"); !errors.Is(err, ErrInvalidMajor) {
		t.Fatalf("mismatched version: %v", err)
	}
}

func TestManagerRejectsInvalidAndConcurrentOperations(t *testing.T) {
	m, _, _ := newTestManager(t, "18.6.0")
	ctx := context.Background()

	for _, bad := range []string{"13", "19", "", "18.6"} {
		if err := m.Activate(ctx, bad); !errors.Is(err, ErrInvalidMajor) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if err := m.Update(ctx, "18"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("update: %v", err)
	}
	if err := m.Activate(ctx, "16"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("activate: %v", err)
	}
	if err := m.Uninstall(ctx, "16", false); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("uninstall: %v", err)
	}

	done, err := m.begin("18", "installing")
	if err != nil {
		t.Fatal(err)
	}
	if b := major(t, m.Versions(), "18").Busy; b != "installing" {
		t.Fatalf("busy %q", b)
	}
	if err := m.Install(ctx, "17", ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent: %v", err)
	}
	done()
	if b := major(t, m.Versions(), "18").Busy; b != "" {
		t.Fatalf("busy %q after done", b)
	}
}

func TestManagerUpdateRollsBackWhenNewReleaseDoesNotStart(t *testing.T) {
	m, svc, published := newTestManager(t, "18.4.0")
	ctx := context.Background()
	if err := m.Install(ctx, "18", ""); err != nil {
		t.Fatal(err)
	}

	// Live, but postgres never comes up.
	svc.live = true
	old := ReadyTimeout
	ReadyTimeout = 300 * time.Millisecond
	defer func() { ReadyTimeout = old }()

	published.Store([]string{"18.4.0", "18.6.0"})
	_ = m.Catalog.Check(ctx)
	err := m.Update(ctx, "18")
	if err == nil || !strings.Contains(err.Error(), "kept 18.4.0") {
		t.Fatalf("want rollback, got %v", err)
	}
	if svc.version != "18.4.0" || !m.Installer.Installed("18.4.0") || m.Installer.Installed("18.6.0") {
		t.Fatalf("after rollback: %+v", svc)
	}
	if _, err := os.Stat(DataDir(m.root(), "18")); err != nil {
		t.Fatal("data must survive")
	}
}
