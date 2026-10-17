package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"flock/internal/binaries/binariestest"
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

// current is the configured server, for checks while an update may run in the background.
func (f *fakeService) current() string {
	v, _ := f.Active()
	return v
}

// pgPackage is a fake PostgreSQL package whose initdb creates a cluster of the major it is given
// in the environment-free way: the major is the name of the data directory (-D <dir>).
func pgPackage(t *testing.T) []byte {
	return binariestest.Zip(t, map[string]string{
		"bin/postgres": "#!/bin/sh\n",
		"bin/initdb":   "#!/bin/sh\nmkdir -p \"$2\" && basename \"$2\" > \"$2/PG_VERSION\"\n",
		"bin/pg_ctl":   "#!/bin/sh\n",
		"bin/psql":     "#!/bin/sh\n",
	})
}

func newTestManager(t *testing.T) (*Manager, *fakeService, *binariestest.Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake initdb is a shell script")
	}
	srv := binariestest.New(t)
	root := t.TempDir()
	svc := &fakeService{}
	return NewManager(root, srv.Catalog(filepath.Join(root, "cache")), svc), svc, srv
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
	m, svc, srv := newTestManager(t)
	ctx := context.Background()
	pkg := pgPackage(t)
	srv.Publish(Package, "17.6", 1, pkg, nil)
	srv.Publish(Package, "18.6", 1, pkg, nil)
	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}

	// Every published major is listed, newest first, before anything is installed.
	v := m.Versions()
	if len(v.Majors) != 2 || v.Majors[0].Major != "18" || v.Majors[0].Latest != "18.6" || v.Active != "" || v.CheckedAt == nil {
		t.Fatalf("got %+v", v)
	}

	// The first install becomes active, gets a cluster and the service is switched on.
	if err := m.Install(ctx, "17"); err != nil {
		t.Fatal(err)
	}
	if svc.current() != "17.6-build1" || !svc.enabled {
		t.Fatalf("service: %+v", svc)
	}
	v = m.Versions()
	if got := major(t, v, "17"); v.Active != "17" || !got.Active || !got.HasData || got.Installed != "17.6" || got.DataSize == 0 || got.EOL != "2029-11-08" {
		t.Fatalf("got %+v", got)
	}
	// The client tools come with it.
	if psql, ok := ClientTool(m.root(), "psql"); !ok || psql != filepath.Join(BinDir(m.root(), "17.6-build1"), "psql") {
		t.Fatalf("psql: %q %v", psql, ok)
	}

	// A second major is installed but doesn't take over; its client tools are the newest.
	if err := m.Install(ctx, "18"); err != nil {
		t.Fatal(err)
	}
	if svc.current() != "17.6-build1" || m.Versions().Active != "17" || major(t, m.Versions(), "18").HasData {
		t.Fatalf("18 must not take over: %+v", svc)
	}
	if psql, _ := ClientTool(m.root(), "psql"); !strings.Contains(psql, "18.6-build1") {
		t.Fatalf("psql: %q", psql)
	}

	// Switching creates 18's own cluster and keeps 17's.
	if err := m.Activate(ctx, "18"); err != nil {
		t.Fatal(err)
	}
	v = m.Versions()
	if svc.current() != "18.6-build1" || v.Active != "18" || !major(t, v, "18").HasData || !major(t, v, "17").HasData {
		t.Fatalf("got %+v", v)
	}

	// A newer patch of an inactive major replaces its binaries and keeps its data.
	srv.Withdraw(Package, "17.6")
	srv.Publish(Package, "17.7", 1, pkg, nil)
	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if !major(t, m.Versions(), "17").Update {
		t.Fatal("17 should have an update")
	}
	if err := m.Update(ctx, "17"); err != nil {
		t.Fatal(err)
	}
	if m.Installer.Installed("17.6-build1") || !m.Installer.Installed("17.7-build1") || svc.current() != "18.6-build1" {
		t.Fatalf("update: %+v", svc)
	}
	if got := major(t, m.Versions(), "17"); got.Update || got.Installed != "17.7" || !got.HasData {
		t.Fatalf("got %+v", got)
	}

	// A higher build of the active major's release is an update too, and becomes the active one.
	srv.Publish(Package, "18.6", 2, pkg, nil)
	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if got := major(t, m.Versions(), "18"); !got.Update || got.Latest != "18.6" {
		t.Fatalf("a new build isn't an update: %+v", got)
	}
	if err := m.Install(ctx, "18"); err != nil {
		t.Fatal(err)
	}
	if svc.current() != "18.6-build2" || m.Installer.Installed("18.6-build1") {
		t.Fatalf("new build: %+v", svc)
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
	if svc.current() != "17.7-build1" || !svc.enabled || v.Active != "17" || major(t, v, "18").Installed != "" || !major(t, v, "18").HasData {
		t.Fatalf("got %+v / %+v", svc, v.Majors)
	}

	// With --delete-data the cluster goes too, and nothing is active any more.
	if err := m.Uninstall(ctx, "17", true); err != nil {
		t.Fatal(err)
	}
	v = m.Versions()
	if svc.current() != "" || v.Active != "" || major(t, v, "17").HasData {
		t.Fatalf("got %+v / %+v", svc, v.Majors)
	}
	if _, ok := ClientTool(m.root(), "psql"); ok {
		t.Fatal("no client tools without an installed major")
	}
	// 18's data, kept above, can still be deleted on its own.
	if err := m.Uninstall(ctx, "18", true); err != nil || major(t, m.Versions(), "18").HasData {
		t.Fatalf("delete leftover data: %v", err)
	}
}

func TestManagerReplacesWithdrawnBuilds(t *testing.T) {
	m, svc, srv := newTestManager(t)
	ctx := context.Background()
	pkg := pgPackage(t)
	srv.Publish(Package, "18.6", 1, pkg, nil)
	if err := m.Install(ctx, "18"); err != nil {
		t.Fatal(err)
	}

	// Build 1 is withdrawn and build 2 published: it replaces build 1 without being asked.
	srv.Publish(Package, "18.6", 2, pkg, map[string]string{"1": "crashes on start"})
	if err := m.Check(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for svc.current() != "18.6-build2" || major(t, m.Versions(), "18").Busy != "" {
		if time.Now().After(deadline) {
			t.Fatalf("the withdrawn build wasn't replaced: %+v", major(t, m.Versions(), "18"))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := major(t, m.Versions(), "18"); got.Yanked != "" || got.Update {
		t.Fatalf("after the replacement: %+v", got)
	}

	// A withdrawn build isn't installed.
	srv.Publish(Package, "17.7", 1, pkg, map[string]string{"1": "broken"})
	if err := m.Install(ctx, "17"); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("installed a withdrawn build: %v", err)
	}
}

func TestManagerRejectsInvalidAndConcurrentOperations(t *testing.T) {
	m, _, srv := newTestManager(t)
	ctx := context.Background()
	srv.Publish(Package, "18.6", 1, pgPackage(t), nil)

	for _, bad := range []string{"", "18.6", "abc"} {
		if err := m.Activate(ctx, bad); !errors.Is(err, ErrInvalidMajor) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if err := m.Install(ctx, "13"); !errors.Is(err, ErrInvalidMajor) {
		t.Fatalf("unpublished major: %v", err)
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
	if err := m.Install(ctx, "17"); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent: %v", err)
	}
	done()
	if b := major(t, m.Versions(), "18").Busy; b != "" {
		t.Fatalf("busy %q after done", b)
	}
}

func TestManagerUpdateRollsBackWhenNewReleaseDoesNotStart(t *testing.T) {
	m, svc, srv := newTestManager(t)
	ctx := context.Background()
	pkg := pgPackage(t)
	srv.Publish(Package, "18.4", 1, pkg, nil)
	if err := m.Install(ctx, "18"); err != nil {
		t.Fatal(err)
	}

	// Live, but postgres never comes up.
	svc.mu.Lock()
	svc.live = true
	svc.mu.Unlock()
	old := ReadyTimeout
	ReadyTimeout = 300 * time.Millisecond
	defer func() { ReadyTimeout = old }()

	srv.Withdraw(Package, "18.4")
	srv.Publish(Package, "18.6", 1, pkg, nil)
	err := m.Update(ctx, "18")
	if err == nil || !strings.Contains(err.Error(), "kept 18.4") {
		t.Fatalf("want rollback, got %v", err)
	}
	if svc.current() != "18.4-build1" || !m.Installer.Installed("18.4-build1") || m.Installer.Installed("18.6-build1") {
		t.Fatalf("after rollback: %+v", svc)
	}
	if _, err := os.Stat(DataDir(m.root(), "18")); err != nil {
		t.Fatal("data must survive")
	}
}

func TestIDs(t *testing.T) {
	if v, b, ok := ParseID("18.6-build2"); !ok || v != "18.6" || b != 2 {
		t.Fatal("ParseID")
	}
	if _, _, ok := ParseID("18.6.0"); ok {
		t.Fatal("a zonky version isn't an install ID")
	}
	if MajorOf("18.6-build1") != "18" || MajorOf("18.6") != "18" || MajorOf("18") != "" || VersionOf("x") != "" {
		t.Fatal("MajorOf")
	}
	if !Newer("18.10-build1", "18.9-build3") || !Newer("18.6-build2", "18.6-build1") || Newer("18.6-build1", "18.6-build1") {
		t.Fatal("Newer")
	}
}
