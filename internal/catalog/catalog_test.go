package catalog

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flock/internal/components"
	"flock/internal/config"
	"flock/internal/supervisor"
)

func configMutate(root string) components.Mutate {
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

// fakeKind installs a marker file; release controls when installs finish.
type fakeKind struct {
	release chan error
	latest  map[string]string
}

func (f *fakeKind) kind() *Kind {
	return &Kind{
		ID: "mysql", Name: "MySQL", Category: "Database", DefaultPort: 3306,
		Lines:  []Line{{"9.7", "9.7 LTS"}, {"8.4", "8.4 LTS"}},
		Method: components.MethodDownload, Estimate: "170 MB download",
		Latest: func(context.Context) (map[string]string, error) { return f.latest, nil },
		Install: func(ctx context.Context, dir, version string, r *components.Reporter) error {
			r.Phase(components.PhaseDownloading, 0.5)
			fmt.Fprintf(r.Log, "downloading %s\n", version)
			select {
			case err := <-f.release:
				if err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			return os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
		},
		Spec: func(p Paths, port int) (supervisor.Spec, error) {
			return supervisor.Spec{Path: filepath.Join(p.Dir, "bin", "mysqld"), Args: []string{p.Data, fmt.Sprint(port)}}, nil
		},
		Env: func(port int) string { return fmt.Sprintf("DB_PORT=%d", port) },
	}
}

func waitJob(t *testing.T, m *Manager, id string, done func(Entry) bool) Entry {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
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
	root := t.TempDir()
	f := &fakeKind{release: make(chan error), latest: map[string]string{"9.7": "9.7.2", "8.4": "8.4.11"}}
	m := NewManager(root, []*Kind{f.kind()}, configMutate(root))
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)

	e, _ := m.Catalog().Find("mysql")
	if e.Installed != "" || !e.Available || e.Lines[0].Latest != "9.7.2" || e.DefaultPort != 3306 {
		t.Fatalf("before install: %+v", e)
	}

	if err := m.Start("mysql", "8.4", port); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("mysql", "8.4", port); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
	e = waitJob(t, m, "mysql", func(e Entry) bool { return e.Job != nil && e.Job.Progress == 0.5 })
	if e.Job.Version != "8.4.11" || e.Job.Line != "8.4" || e.Job.Action != "install" || e.Job.State != "running" {
		t.Fatalf("job: %+v", e.Job)
	}
	f.release <- errors.New("503")
	e = waitJob(t, m, "mysql", func(e Entry) bool { return e.Job != nil && e.Job.State == "failed" })
	if e.Job.Error != "503" || e.Installed != "" {
		t.Fatalf("failed job: %+v", e)
	}
	if log, _ := os.ReadFile(m.LogPath("mysql")); !strings.Contains(string(log), "downloading 8.4.11") {
		t.Fatalf("log: %s", log)
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
	f.release <- nil
	m.Wait()
	e, _ = m.Catalog().Find("mysql")
	if e.Installed != "8.4.11" || e.Line != "8.4" || e.Port != port || e.Job != nil || e.Update || !e.Lines[1].Installed {
		t.Fatalf("installed: %+v", e)
	}
	cfg, _ := config.Load(root)
	if x, ok := cfg.Services.Extra("mysql"); !ok || x.Version != "8.4.11" || x.Port != port || !cfg.Services.Running("mysql") {
		t.Fatalf("config: %+v", cfg.Services)
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
	f.latest = map[string]string{"9.7": "9.7.2", "8.4": "8.4.12"}
	_ = m.Check(context.Background())
	if e, _ = m.Catalog().Find("mysql"); !e.Update || e.Latest != "8.4.12" {
		t.Fatalf("update: %+v", e)
	}
	if err := m.Start("mysql", "", 0); err != nil {
		t.Fatal(err)
	}
	if e, _ = m.Catalog().Find("mysql"); e.Job == nil || e.Job.Action != "update" {
		t.Fatalf("update job: %+v", e.Job)
	}
	f.release <- nil
	m.Wait()
	if e, _ = m.Catalog().Find("mysql"); e.Installed != "8.4.12" || e.Port != port {
		t.Fatalf("updated: %+v", e)
	}
	if _, err := os.Stat(Dir(root, "mysql", "8.4.11")); !os.IsNotExist(err) {
		t.Fatal("old version kept")
	}

	// Switching lines replaces the binaries; each line keeps its data.
	if err := m.Start("mysql", "9.7", 0); err != nil {
		t.Fatal(err)
	}
	f.release <- nil
	m.Wait()
	e, _ = m.Catalog().Find("mysql")
	if e.Installed != "9.7.2" || e.Line != "9.7" || !e.Lines[1].HasData || e.Lines[0].HasData {
		t.Fatalf("switched: %+v", e)
	}

	// Uninstalling keeps the data unless asked.
	if err := m.Uninstall("mysql", false); err != nil {
		t.Fatal(err)
	}
	if e, _ = m.Catalog().Find("mysql"); e.Installed != "" || !e.Lines[1].HasData {
		t.Fatalf("uninstalled: %+v", e)
	}
	if cfg, _ := config.Load(root); len(cfg.Services.Extras) != 0 {
		t.Fatalf("config keeps %+v", cfg.Services.Extras)
	}
	if err := m.Uninstall("mysql", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "services", "mysql")); !os.IsNotExist(err) {
		t.Fatal("data kept")
	}
}

func TestCancelForgetsTheJob(t *testing.T) {
	root := t.TempDir()
	f := &fakeKind{release: make(chan error), latest: map[string]string{"9.7": "9.7.2"}}
	m := NewManager(root, []*Kind{f.kind()}, configMutate(root))
	_ = m.Check(context.Background()) // fresh, so reading the catalog doesn't check in the background
	if err := m.Start("mysql", "", freePort(t)); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, "mysql", func(e Entry) bool { return e.Job != nil && e.Job.Version == "9.7.2" })
	if err := m.Cancel("mysql"); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	if e, _ := m.Catalog().Find("mysql"); e.Job != nil || e.Installed != "" {
		t.Fatalf("after cancel: %+v", e)
	}
}

func TestStartRejectsBadRequests(t *testing.T) {
	root := t.TempDir()
	f := &fakeKind{release: make(chan error), latest: map[string]string{}}
	off := f.kind()
	off.ID, off.Method, off.Unavailable = "typesense", "", "no Windows build"
	m := NewManager(root, []*Kind{f.kind(), off}, configMutate(root))
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
}

func TestSuggestPortSkipsFlockServicesAndBusyPorts(t *testing.T) {
	cfg := config.Default()
	cfg.Services.Valkey = "9.1.2"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	busy := ln.Addr().(*net.TCPAddr).Port

	if got := SuggestPort(cfg, "redis", cfg.Ports.Valkey); got == cfg.Ports.Valkey {
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

func TestLineOf(t *testing.T) {
	k := &Kind{Lines: []Line{{"8.10", ""}, {"8.4", ""}, {"8", ""}, {"30", ""}, {"1", ""}}}
	for v, want := range map[string]string{
		"8.10.2": "8.10", "8.4.7": "8.4", "8.1.0": "8", "30.2": "30", "1.55.0": "1", "1.0.0-alpha.3": "1", "7.2.16": "", "80.1": "",
	} {
		if got := k.LineOf(v); got != want {
			t.Errorf("LineOf(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestNewestByLineSkipsPreReleasesAndOtherLines(t *testing.T) {
	k := &Kind{Lines: []Line{{"8.10", ""}, {"7.4", ""}}}
	got := newestByLine(k, [][]string{{"", "8.10.1"}, {"", "8.10.10"}, {"", "8.10.2"}, {"", "8.12-rc1"}, {"", "v7.4.3"}, {"", "7.2.16"}, {"", "8.1.0"}})
	if got["8.10"] != "8.10.10" || got["7.4"] != "7.4.3" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestProbeFindsTheNewestPatch(t *testing.T) {
	present := map[string]bool{"/9.7.2": true, "/9.7.3": true, "/9.7.5": true, "/9.7.8": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || !present[r.URL.Path] {
			http.Error(w, "no", http.StatusForbidden)
		}
	}))
	defer srv.Close()

	// One gap is skipped (9.7.4), two end the search (9.7.6, 9.7.7).
	got, err := probeLines(context.Background(), map[string]string{"9.7": "9.7.2"}, func(v string) string { return srv.URL + "/" + v })
	if err != nil || got["9.7"] != "9.7.5" {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestChecksumLists(t *testing.T) {
	sha := strings.Repeat("ab", 32)
	if got, err := sumFor([]byte(sha+"  mongodb-macos-arm64-8.0.34.tgz\n"), "mongodb-macos-arm64-8.0.34.tgz"); err != nil || got != sha {
		t.Fatalf("sha256sum line: %q %v", got, err)
	}
	if got, err := sumFor([]byte(strings.ToUpper(sha)+" *./mariadb-11.8.9-winx64.zip\n"), "mariadb-11.8.9-winx64.zip"); err != nil || got != sha {
		t.Fatalf("binary-mode line: %q %v", got, err)
	}
	if _, err := sumFor([]byte(sha+"  other.zip\n"+sha+"  another.zip"), "mine.zip"); err == nil {
		t.Fatal("found a checksum for another file")
	}
	readme := "hash redis-8.10.2.tar.gz sha256 " + sha + " http://download.redis.io/releases/redis-8.10.2.tar.gz\n"
	if got, err := parseHashes([]byte(readme), "redis-8.10.2.tar.gz"); err != nil || got != sha {
		t.Fatalf("redis-hashes: %q %v", got, err)
	}
	if _, err := parseHashes([]byte(readme), "redis-8.10.1.tar.gz"); err == nil {
		t.Fatal("found another version's checksum")
	}
	md := strings.Repeat("cd", 16)
	if got, err := md5For([]byte(md+"  mysql-9.7.2-macos15-arm64.tar.gz"), "mysql-9.7.2-macos15-arm64.tar.gz"); err != nil || got != md {
		t.Fatalf("md5: %q %v", got, err)
	}
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if strings.HasSuffix(name, "/") {
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755})
			continue
		}
		if target, ok := strings.CutPrefix(files[name], "->"); ok {
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target})
			continue
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(files[name]))})
		_, _ = tw.Write([]byte(files[name]))
	}
	_ = tw.Close()
	_ = gz.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnpackDropsTheTopDirectory(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "mysql.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"mysql-9.7.2/":                  "",
		"mysql-9.7.2/bin/mysqld":        "server",
		"mysql-9.7.2/lib/libssl.dylib":  "ssl",
		"mysql-9.7.2/lib/libssl.3.link": "->libssl.dylib",
	})
	out := filepath.Join(dir, "out")
	if err := unpack(archive, out, ""); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(out, "bin", "mysqld")); string(data) != "server" {
		t.Fatalf("bin/mysqld holds %q", data)
	}
	if info, err := os.Stat(filepath.Join(out, "bin", "mysqld")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("mysqld isn't executable: %v %v", info, err)
	}
	if target, _ := os.Readlink(filepath.Join(out, "lib", "libssl.3.link")); target != "libssl.dylib" {
		t.Fatalf("symlink: %q", target)
	}
}

func TestUnpackZipWithFilesAtTheRootAndBareBinaries(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "rustfs.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"rustfs.exe", "LICENSE"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(name))
	}
	_ = zw.Close()
	_ = os.WriteFile(archive, buf.Bytes(), 0o644)
	out := filepath.Join(dir, "out")
	if err := unpack(archive, out, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "rustfs.exe")); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(dir, "meilisearch-macos-apple-silicon")
	_ = os.WriteFile(binary, []byte("meili"), 0o644)
	out = filepath.Join(dir, "meili")
	if err := unpack(binary, out, "meilisearch"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(out, "meilisearch")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary: %v %v", info, err)
	}
}

func TestUnpackRefusesEntriesOutsideTheTarget(t *testing.T) {
	dir := t.TempDir()
	for name, files := range map[string]map[string]string{
		"dotdot.tar.gz":  {"../evil": "x"},
		"symlink.tar.gz": {"a/link": "->../../evil"},
	} {
		archive := filepath.Join(dir, name)
		writeTarGz(t, archive, files)
		if err := unpack(archive, filepath.Join(dir, "out-"+name), ""); err == nil {
			t.Errorf("%s: unpacked", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("wrote outside the target")
	}
}

func TestTypesenseIsCheckedAgainstTheMD5InItsArchive(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "typesense-server"), []byte("server"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "typesense-server.md5.txt"), []byte(strings.Repeat("0", 32)+"  typesense-server\n"), 0o644)
	if err := checkTypesenseMD5(dir); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("a wrong MD5 passed: %v", err)
	}
	sum := md5.Sum([]byte("server"))
	_ = os.WriteFile(filepath.Join(dir, "typesense-server.md5.txt"), []byte(hex.EncodeToString(sum[:])+"\n"), 0o644)
	if err := checkTypesenseMD5(dir); err != nil {
		t.Fatal(err)
	}
}

func TestKindsPerPlatform(t *testing.T) {
	ids := func(kinds []*Kind) []string {
		var out []string
		for _, k := range kinds {
			if k.Method != "" {
				out = append(out, k.ID)
			} else if k.Unavailable == "" {
				t.Errorf("%s is unavailable without a reason", k.ID)
			}
		}
		return out
	}
	all := []string{"mysql", "mariadb", "mongodb", "redis", "meilisearch", "typesense", "rustfs"}
	if got := ids(Kinds("darwin", "arm64")); !slices.Equal(got, all) {
		t.Errorf("darwin/arm64: %v", got)
	}
	if got := ids(Kinds("darwin", "amd64")); slices.Contains(got, "rustfs") || len(got) != 6 {
		t.Errorf("darwin/amd64: %v", got)
	}
	if got := ids(Kinds("windows", "amd64")); slices.Contains(got, "typesense") || len(got) != 6 {
		t.Errorf("windows/amd64: %v", got)
	}
	if got := ids(Kinds("windows", "arm64")); len(got) != 0 {
		t.Errorf("windows/arm64: %v", got)
	}
	for _, k := range Kinds("darwin", "arm64") {
		if k.Name == "" || k.Category == "" || k.Description == "" || k.DefaultPort == 0 || len(k.Lines) == 0 ||
			k.Latest == nil || k.Install == nil || k.Spec == nil || k.Env == nil || k.Estimate == "" {
			t.Errorf("incomplete kind %+v", k)
		}
		if k.Env(1234) == "" || !strings.Contains(k.Env(1234), "1234") {
			t.Errorf("%s env: %q", k.ID, k.Env(1234))
		}
	}
}

func TestSpecsBindToLocalhost(t *testing.T) {
	root := t.TempDir()
	for _, k := range Kinds("darwin", "arm64") {
		if k.ID == "mysql" || k.ID == "mariadb" {
			continue // they initialise their data with the real server first
		}
		version := k.Lines[0].ID + ".0"
		if strings.Count(version, ".") < 2 {
			version += ".0"
		}
		p := PathsOf(root, k, version)
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

func TestMariaDBChecksumsFromTheReleaseAPI(t *testing.T) {
	sha := strings.Repeat("ef", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest-api/mariadb/11.8/latest/":
			fmt.Fprint(w, `{"releases":{"11.8.9":{"files":[]}}}`)
		case "/rest-api/mariadb/11.8.9/":
			fmt.Fprintf(w, `{"release_data":{"11.8.9":{"files":[{"file_name":"repo/","checksum":{"sha256sum":null}},
				{"file_name":"mariadb-11.8.9.tar.gz","checksum":{"sha256sum":"%s"}}]}}}`, sha)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := HTTPClient
	HTTPClient = &http.Client{Transport: rewrite{srv.URL}}
	defer func() { HTTPClient = old }()

	if v, err := mariadbLatest(context.Background(), "11.8"); err != nil || v != "11.8.9" {
		t.Fatalf("latest: %q %v", v, err)
	}
	if got, err := mariadbSHA256(context.Background(), "11.8.9", "mariadb-11.8.9.tar.gz"); err != nil || got != sha {
		t.Fatalf("sha: %q %v", got, err)
	}
	if _, err := mariadbSHA256(context.Background(), "11.8.9", "mariadb-11.8.9-winx64.zip"); err == nil {
		t.Fatal("found a checksum for a file that isn't listed")
	}
}

// rewrite sends every request to a test server, keeping the path.
type rewrite struct{ base string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	target, _ := http.NewRequestWithContext(req.Context(), req.Method, r.base+u.Path, req.Body)
	return http.DefaultTransport.RoundTrip(target)
}
