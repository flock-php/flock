package catalog

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/binaries"
	"flock/internal/binaries/binariestest"
	"flock/internal/config"
	"flock/internal/supervisor"
)

func configMutate(root string) Mutate {
	return func(change func(*config.Config) error) error {
		cfg, err := config.Load(root)
		if err != nil {
			return err
		}
		if err := change(cfg); err != nil {
			return err
		}
		return cfg.Save(root)
	}
}

// testKind is a MySQL-like service whose package needs bin/mysqld.
func testKind() *Kind {
	return &Kind{
		ID: "mysql", Name: "MySQL", Category: "Database", DefaultPort: 3306, Line: majorMinor,
		Prepare: needs("bin/mysqld"),
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			return supervisor.Spec{Path: filepath.Join(p.Dir, "bin", "mysqld"), Args: []string{p.Data, fmt.Sprint(port)}}, nil
		},
		Env: func(port int) string { return fmt.Sprintf("DB_PORT=%d", port) },
	}
}

// mysqlPackage is a package of testKind; a second top-level directory keeps bin/ from being unwrapped.
func mysqlPackage(t *testing.T, version string) []byte {
	return binariestest.Zip(t, map[string]string{"bin/mysqld": version, "share/errmsg.sys": "x"})
}

func newTestManager(t *testing.T, kinds ...*Kind) (*Manager, *binariestest.Server, string) {
	t.Helper()
	if binaries.Platform() == "" {
		t.Skip("no packages for this platform")
	}
	srv := binariestest.New(t)
	root := t.TempDir()
	return NewManager(root, kinds, srv.Catalog(filepath.Join(root, "cache")), configMutate(root)), srv, root
}

func waitJob(t *testing.T, m *Manager, id string, done func(Entry) bool) Entry {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		e, _ := m.Catalog().Find(id)
		if done(e) {
			return e
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %+v", e)
		}
	}
}

// freePort returns a port that is free right now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestInstallUpdateSwitchAndUninstall(t *testing.T) {
	m, srv, root := newTestManager(t, testKind())
	srv.Publish("mysql", "9.7.2", 1, mysqlPackage(t, "9.7.2"), nil)
	srv.Publish("mysql", "8.4.11", 1, mysqlPackage(t, "8.4.11"), nil)
	srv.Publish("mysql", "5.1.0", 1, binariestest.Zip(t, map[string]string{"README": "no server", "docs/x": "x"}), nil)
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)

	e, _ := m.Catalog().Find("mysql")
	if e.Installed != "" || !e.Available || len(e.Lines) != 3 || e.Lines[0].ID != "9.7" ||
		e.Lines[0].Latest != "9.7.2" || e.DefaultPort != 3306 || !strings.HasSuffix(e.Estimate, "MB download") {
		t.Fatalf("before install: %+v", e)
	}

	// A package that doesn't have what the service needs fails, and leaves nothing behind.
	if err := m.Start("mysql", "5.1", port); err != nil {
		t.Fatal(err)
	}
	e = waitJob(t, m, "mysql", func(e Entry) bool { return e.Job != nil && e.Job.State == "failed" })
	if !strings.Contains(e.Job.Error, "bin/mysqld") || e.Installed != "" || e.Job.Version != "5.1.0" {
		t.Fatalf("failed job: %+v", e)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "services", "mysql")); len(entries) != 0 {
		t.Fatalf("a failed install leaves %v", entries)
	}
	if err := m.Dismiss("mysql"); err != nil {
		t.Fatal(err)
	}

	if err := m.Start("mysql", "8.4", port); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	e, _ = m.Catalog().Find("mysql")
	if e.Installed != "8.4.11" || e.Line != "8.4" || e.Port != port || e.Job != nil || e.Update || !e.Lines[1].Installed {
		t.Fatalf("installed: %+v", e)
	}
	cfg, _ := config.Load(root)
	if x, ok := cfg.Services.Extra("mysql"); !ok || x.Version != "8.4.11" || x.Build != 1 || x.Port != port || !cfg.Services.Running("mysql") {
		t.Fatalf("config: %+v", cfg.Services)
	}
	if log, _ := os.ReadFile(m.LogPath("mysql")); !strings.Contains(string(log), "installed MySQL 8.4.11") {
		t.Fatalf("log: %s", log)
	}

	// The spec runs it with the line's data directory, which a running server fills.
	spec, err := Spec(root, m.kinds[0], config.Extra{Version: "8.4.11", Port: port})
	if err != nil || spec.Name != "mysql" || spec.Args[0] != DataDir(root, "mysql", "8.4") || spec.LogFile != LogFile(root, "mysql") {
		t.Fatalf("spec: %+v %v", spec, err)
	}
	if err := os.WriteFile(filepath.Join(DataDir(root, "mysql", "8.4"), "ibdata1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// A newer patch is an update within the line; the port stays.
	srv.Withdraw("mysql", "8.4.11")
	srv.Publish("mysql", "8.4.12", 1, mysqlPackage(t, "8.4.12"), nil)
	_ = m.Check(context.Background())
	if e, _ = m.Catalog().Find("mysql"); !e.Update || e.Latest != "8.4.12" {
		t.Fatalf("update: %+v", e)
	}
	if err := m.Start("mysql", "", 0); err != nil {
		t.Fatal(err)
	}
	if e, _ = m.Catalog().Find("mysql"); e.Job != nil && e.Job.Action != "update" {
		t.Fatalf("update job: %+v", e.Job)
	}
	m.Wait()
	if e, _ = m.Catalog().Find("mysql"); e.Installed != "8.4.12" || e.Port != port {
		t.Fatalf("updated: %+v", e)
	}
	if _, err := os.Stat(Dir(root, "mysql", "8.4.11")); !os.IsNotExist(err) {
		t.Fatal("old version kept")
	}

	// A higher build of the installed version is an update too, and replaces it in place.
	srv.Publish("mysql", "8.4.12", 2, mysqlPackage(t, "8.4.12 build 2"), nil)
	_ = m.Check(context.Background())
	if e, _ = m.Catalog().Find("mysql"); !e.Update || e.Latest != "8.4.12" {
		t.Fatalf("a new build isn't an update: %+v", e)
	}
	if err := m.Start("mysql", "", 0); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	if data, _ := os.ReadFile(filepath.Join(Dir(root, "mysql", "8.4.12"), "bin", "mysqld")); string(data) != "8.4.12 build 2" {
		t.Fatalf("not replaced: %q", data)
	}
	if cfg, _ := config.Load(root); cfg.Services.Extras["mysql"].Build != 2 || !cfg.Services.Running("mysql") {
		t.Fatalf("config after the new build: %+v", cfg.Services)
	}

	// Another line installs next to the active one, which keeps running; a port for it is refused
	// (the port belongs to the service).
	if err := m.Start("mysql", "9.7", port+1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another port for another line: %v", err)
	}
	if err := m.Start("mysql", "9.7", 0); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	e, _ = m.Catalog().Find("mysql")
	if e.Installed != "8.4.12" || !e.Lines[0].Installed || e.Lines[0].Active || e.Lines[0].Version != "9.7.2" || !e.Lines[1].Active {
		t.Fatalf("installed next to it: %+v", e)
	}
	if data, _ := os.ReadFile(filepath.Join(Dir(root, "mysql", "9.7.2"), buildFile)); string(data) != "1" {
		t.Fatalf("build file: %q", data)
	}

	// Switching runs the other line on the same port; each line keeps its data. The build of a
	// version installed before builds were recorded on disk (only the config knows it) is kept.
	if err := os.Remove(filepath.Join(Dir(root, "mysql", "8.4.12"), buildFile)); err != nil {
		t.Fatal(err)
	}
	if err := m.Activate("mysql", "5.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("activated a line that isn't installed: %v", err)
	}
	if err := m.Activate("mysql", "9.7"); err != nil {
		t.Fatal(err)
	}
	e, _ = m.Catalog().Find("mysql")
	if e.Installed != "9.7.2" || e.Line != "9.7" || e.Port != port || !e.Lines[0].Active || !e.Lines[1].Installed || !e.Lines[1].HasData || e.Lines[0].HasData {
		t.Fatalf("switched: %+v", e)
	}
	if cfg, _ := config.Load(root); cfg.Services.Extras["mysql"].Build != 1 || !cfg.Services.Running("mysql") {
		t.Fatalf("config after switching: %+v", cfg.Services)
	}

	// Uninstalling the active line hands over to the other one; the data stays unless asked.
	if err := m.Uninstall("mysql", "9.7", false); err != nil {
		t.Fatal(err)
	}
	e, _ = m.Catalog().Find("mysql")
	if e.Installed != "8.4.12" || e.Port != port || e.Lines[0].Installed {
		t.Fatalf("uninstalled 9.7: %+v", e)
	}
	if cfg, _ := config.Load(root); cfg.Services.Extras["mysql"].Build != 2 {
		t.Fatalf("the remaining line's build: %+v", cfg.Services)
	}
	if err := m.Uninstall("mysql", "9.7", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uninstalled it twice: %v", err)
	}

	// The last line goes too; a line with data stays listed.
	if err := m.Uninstall("mysql", "8.4", false); err != nil {
		t.Fatal(err)
	}
	if e, _ = m.Catalog().Find("mysql"); e.Installed != "" || !e.Lines[1].HasData || e.Lines[1].Installed {
		t.Fatalf("uninstalled: %+v", e)
	}
	if cfg, _ := config.Load(root); len(cfg.Services.Extras) != 0 {
		t.Fatalf("config keeps %+v", cfg.Services.Extras)
	}
	if err := m.Uninstall("mysql", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "mysql")); !os.IsNotExist(err) {
		t.Fatal("data kept")
	}
}

func TestWithdrawnBuildsAreReplaced(t *testing.T) {
	m, srv, root := newTestManager(t, testKind())
	srv.Publish("mysql", "8.4.11", 1, mysqlPackage(t, "build 1"), nil)
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("mysql", "8.4", freePort(t)); err != nil {
		t.Fatal(err)
	}
	m.Wait()

	// Build 1 is withdrawn and build 2 published: it replaces build 1 without being asked.
	srv.Publish("mysql", "8.4.11", 2, mysqlPackage(t, "build 2"), map[string]string{"1": "crashes on start"})
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, "mysql", func(e Entry) bool {
		cfg, _ := config.Load(root)
		return e.Job == nil && cfg.Services.Extras["mysql"].Build == 2
	})
	if e, _ := m.Catalog().Find("mysql"); e.Yanked != "" || e.Update {
		t.Fatalf("after the replacement: %+v", e)
	}

	// A withdrawn build isn't installed.
	srv.Publish("mysql", "9.7.2", 1, mysqlPackage(t, "9.7.2"), map[string]string{"1": "broken"})
	_ = m.Check(context.Background())
	if err := m.Start("mysql", "9.7", 0); err == nil || !strings.Contains(err.Error(), "withdrawn") {
		t.Fatalf("installed a withdrawn build: %v", err)
	}
}

func TestStartRejectsBadRequests(t *testing.T) {
	off := testKind()
	off.ID, off.Name, off.Unavailable = "typesense", "Typesense", "no Windows build"
	m, srv, root := newTestManager(t, testKind(), off)
	srv.Publish("mysql", "9.7.2", 1, mysqlPackage(t, "9.7.2"), nil)
	_ = m.Check(context.Background())

	cfg := config.Default()
	_ = cfg.Save(root)
	for _, c := range []struct {
		id, line string
		port     int
		want     error
	}{
		{"memcached", "", 0, ErrUnknown},
		{"typesense", "", 0, ErrUnavailable},
		{"mysql", "5.7", 0, ErrInvalid},
		{"mysql", "", 80, ErrInvalid},
		{"mysql", "", cfg.Ports.SMTP, ErrInvalid}, // Flock's mail catcher has it
	} {
		if err := m.Start(c.id, c.line, c.port); !errors.Is(err, c.want) {
			t.Errorf("Start(%q, %q, %d) = %v, want %v", c.id, c.line, c.port, err, c.want)
		}
	}
	if e, _ := m.Catalog().Find("typesense"); e.Available || e.Unavailable != "no Windows build" {
		t.Fatalf("unavailable entry: %+v", e)
	}
	if ok, why := m.Available(off); ok || why != "no Windows build" {
		t.Fatalf("Available: %v %q", ok, why)
	}
}

func TestSuggestPortSkipsFlockServicesAndBusyPorts(t *testing.T) {
	cfg := config.Default()
	valkeyPort := freePort(t)
	cfg.Services.SetExtra("valkey", config.Extra{Version: "9.1.2", Port: valkeyPort})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	if got := SuggestPort(cfg, "redis", valkeyPort); got == valkeyPort {
		t.Fatalf("suggested Valkey's port %d", got)
	}
	if got := SuggestPort(cfg, "redis", busy); got == busy {
		t.Fatalf("suggested the busy port %d", got)
	}
	// Another service's configured port is taken even while it isn't running; its own isn't.
	mine := freePort(t)
	cfg.Services.SetExtra("mysql", config.Extra{Version: "8.4.11", Port: mine})
	if got := SuggestPort(cfg, "mariadb", mine); got == mine {
		t.Fatal("suggested MySQL's port for MariaDB")
	}
	if _, taken := cfg.UsedPorts("mysql")[mine]; taken {
		t.Fatal("MySQL's own port counts as taken for MySQL")
	}
}

func TestLines(t *testing.T) {
	for v, want := range map[string]string{"8.10.2": "8.10", "8.4.7": "8.4", "11.8.9": "11.8", "8": "", "abc.1": ""} {
		if got := majorMinor(v); got != want {
			t.Errorf("majorMinor(%q) = %q, want %q", v, got, want)
		}
	}
	for v, want := range map[string]string{"30.2": "30", "1.55.0": "1", "1.0.0-alpha.3": "1", "x": ""} {
		if got := major(v); got != want {
			t.Errorf("major(%q) = %q, want %q", v, got, want)
		}
	}
	if !newer("8.4.12", 1, "8.4.11", 3) || !newer("8.4.11", 2, "8.4.11", 1) || newer("8.4.11", 1, "8.4.11", 1) || !newer("8.4.11", 1, "8.4.11", 0) {
		t.Fatal("newer")
	}
}

// examples are a version of each service, for the specs.
var examples = map[string]string{"mysql": "9.7.2", "mariadb": "11.8.9", "mongodb": "8.0.34", "valkey": "9.1.2", "redis": "8.4.7",
	"meilisearch": "1.55.0", "typesense": "30.2", "rustfs": "1.0.2"}

func TestKinds(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		kinds := Kinds(goos)
		if len(kinds) != len(examples) {
			t.Fatalf("%s: %d kinds", goos, len(kinds))
		}
		for _, k := range kinds {
			if k.Name == "" || k.Category == "" || k.Description == "" || k.DefaultPort == 0 || k.Line == nil || k.Spec == nil || k.Env == nil ||
				k.LineOf(examples[k.ID]) == "" {
				t.Errorf("incomplete kind %+v", k)
			}
			if k.Env(1234) == "" || !strings.Contains(k.Env(1234), "1234") {
				t.Errorf("%s env: %q", k.ID, k.Env(1234))
			}
		}
	}
	for _, k := range Kinds("windows") {
		if (k.ID == "redis" || k.ID == "typesense") && k.Unavailable == "" {
			t.Errorf("%s says nothing about Windows", k.ID)
		}
	}
}

func TestSpecsBindToLocalhost(t *testing.T) {
	root := t.TempDir()
	for _, goos := range []string{"darwin", "windows"} {
		for _, k := range Kinds(goos) {
			if k.ID == "mysql" || k.ID == "mariadb" {
				continue // they initialise their data with the real server first
			}
			p := PathsOf(root, k, examples[k.ID])
			spec, err := k.Spec(p, 4567)
			if err != nil {
				t.Fatalf("%s: %v", k.ID, err)
			}
			args := strings.Join(spec.Args, " ")
			if !strings.Contains(args, "127.0.0.1") || !strings.Contains(args, "4567") || !strings.HasPrefix(spec.Path, p.Dir) {
				t.Errorf("%s: %s %s", k.ID, spec.Path, args)
			}
			if k.ID == "typesense" && !strings.Contains(args, "--peering-address=127.0.0.1") {
				t.Errorf("typesense peers on the LAN: %s", args)
			}
			if k.ID == "valkey" && goos == "windows" {
				// The MSYS2 build reads C:/… paths, and has to be asked to save before it stops.
				if strings.Contains(args, `\`) || spec.Stop == nil || !strings.HasSuffix(spec.Path, "valkey-server.exe") {
					t.Errorf("valkey on Windows: %s %s", spec.Path, args)
				}
			}
		}
	}
}

func TestSQLSpecInitialisesOnce(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root, Dir: filepath.Join(root, "services", "mysql", "9.7.2"), Data: DataDir(root, "mysql", "9.7")}
	_ = os.MkdirAll(p.Data, 0o700)
	calls := 0
	// Like mysqld --initialize, it creates the data directory itself.
	initialise := func() error {
		calls++
		_ = os.MkdirAll(p.Data, 0o700)
		return os.WriteFile(filepath.Join(p.Data, "ibdata1"), nil, 0o600)
	}
	for range 2 {
		spec, err := sqlSpec("darwin", p, 3307, "mysqld", "mysqladmin", initialise, "--mysqlx=OFF")
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Join(spec.Args, " ")
		if !strings.Contains(args, "--port=3307") || !strings.Contains(args, "--bind-address=127.0.0.1") || !strings.Contains(args, "--mysqlx=OFF") ||
			!strings.Contains(args, "--socket="+filepath.Join(root, "run", "mysqld.sock")) {
			t.Fatalf("args: %s", args)
		}
	}
	if calls != 1 {
		t.Fatalf("initialised %d times", calls)
	}

	// A failed initialisation leaves no half-made data behind.
	other := p
	other.Data = DataDir(root, "mysql", "8.4")
	if _, err := sqlSpec("darwin", other, 3307, "mysqld", "mysqladmin", func() error {
		_ = os.MkdirAll(other.Data, 0o700)
		_ = os.WriteFile(filepath.Join(other.Data, "half"), nil, 0o600)
		return errors.New("boom")
	}); err == nil {
		t.Fatal("want the error")
	}
	if _, err := os.Stat(other.Data); !os.IsNotExist(err) {
		t.Fatal("half-made data kept")
	}
}

func TestPreparesTrimWhatFlockDoesntNeed(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"bin/mysqld", "bin/mysqld-debug", "lib/libmysql.a", "mysql-test/x", "lib/plugin/debug/x.so", "share/english/errmsg.sys"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		_ = os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	if err := mysql("darwin").Prepare(dir); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]bool{"bin/mysqld": true, "share/english/errmsg.sys": true, "bin/mysqld-debug": false, "lib/libmysql.a": false,
		"mysql-test": false, "lib/plugin/debug": false} {
		if _, err := os.Stat(filepath.Join(dir, f)); (err == nil) != want {
			t.Errorf("%s: kept %v, want %v", f, err == nil, want)
		}
	}
	if err := valkey("darwin").Prepare(dir); err == nil || !strings.Contains(err.Error(), "valkey-server") {
		t.Fatalf("a Valkey package without valkey-server: %v", err)
	}
}
