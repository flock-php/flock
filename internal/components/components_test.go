package components

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseGitHubReleases(t *testing.T) {
	data := `[
		{"tag_name": "9.2.0-rc1", "prerelease": true, "draft": false},
		{"tag_name": "9.0.6", "prerelease": false, "draft": false},
		{"tag_name": "9.1.2", "prerelease": false, "draft": false},
		{"tag_name": "9.1.10", "prerelease": false, "draft": false},
		{"tag_name": "9.3.0", "prerelease": false, "draft": true},
		{"tag_name": "8.1.10", "prerelease": false, "draft": false}
	]`
	got, err := ParseGitHubReleases([]byte(data))
	if err != nil || got != "9.1.10" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := ParseGitHubReleases([]byte(`[{"tag_name":"9.2.0-rc1","prerelease":true}]`)); err == nil {
		t.Fatal("only pre-releases must fail")
	}
	if _, err := ParseGitHubReleases([]byte(`{"message":"API rate limit exceeded"}`)); err == nil {
		t.Fatal("an error object must fail")
	}
}

func TestParseValkeyHashes(t *testing.T) {
	readme := "Valkey release hashes\n\n" +
		"hash valkey-9.1.1.tar.gz sha256 7d7232acd1b8a49b4e05d07a00b3ca8c801ae06ab633ca6a3423bc5f385ab7ee https://github.com/valkey-io/valkey/archive/refs/tags/9.1.1.tar.gz\n" +
		"hash valkey-9.1.2.tar.gz sha256 19C23908E7D57E8D91EF85B41F5646307582F10F4F0FB999BBF89ED24EC9C983 https://github.com/valkey-io/valkey/archive/refs/tags/9.1.2.tar.gz\n"
	got, err := ParseValkeyHashes([]byte(readme), "9.1.2")
	if err != nil || got != "19c23908e7d57e8d91ef85b41f5646307582f10f4f0fb999bbf89ed24ec9c983" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := ParseValkeyHashes([]byte(readme), "9.1.21"); err == nil {
		t.Fatal("unknown version must fail")
	}
}

func TestParsePostgresVersions(t *testing.T) {
	data := `[{"major": "9.6", "latestMinor": "24"}, {"major": "17", "latestMinor": "11"}, {"major": "18", "latestMinor": "6", "current": true}]`
	if got, err := ParsePostgresVersions([]byte(data), "18"); err != nil || got != "18.6" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := ParsePostgresVersions([]byte(data), "19"); err == nil {
		t.Fatal("unknown major must fail")
	}
	src := DefaultSources()
	if got := src.PostgresSourceURL("18.6"); got != "https://ftp.postgresql.org/pub/source/v18.6/postgresql-18.6.tar.bz2" {
		t.Fatal(got)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"9.1.10", "9.1.9", true}, {"18.10", "18.9", true}, {"9.1.2", "9.1.2", false}, {"8.1.10", "9.0.0", false}, {"18.6", "18.6.0", false}} {
		if Newer(c.a, c.b) != c.want {
			t.Fatalf("Newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
}

// fakeDriver installs whatever it is told to, when the test releases it.
type fakeDriver struct {
	mu        sync.Mutex
	installed string
	latest    string
	latestErr error
	release   chan error
}

func (f *fakeDriver) driver(id string) *Driver {
	return &Driver{
		ID: id, Name: "Thing " + id, Method: MethodBuild, Estimate: "about 1 minute",
		Installed: func() string {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.installed
		},
		Latest: func(context.Context) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.latest, f.latestErr
		},
		Install: func(ctx context.Context, version string, r *Reporter) error {
			r.Phase(PhaseDownloading, 0.5)
			r.Log.Write([]byte("compiling " + version + "\n"))
			r.Phase(PhaseBuilding, -1)
			select {
			case err := <-f.release:
				if err != nil {
					return err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			f.mu.Lock()
			f.installed = version
			f.mu.Unlock()
			return nil
		},
		Uninstall: func() error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.installed = ""
			return nil
		},
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("timed out")
}

func TestJobLifecycle(t *testing.T) {
	f := &fakeDriver{latest: "9.1.2", release: make(chan error)}
	m := NewManager(t.TempDir(), []*Driver{f.driver(Valkey)})
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := m.Start(Valkey); err != nil {
		t.Fatal(err)
	}
	c, _ := m.Components().Find(Valkey)
	if c.Job == nil || c.Job.State != "running" || c.Job.Action != "install" || c.Job.StartedAt.IsZero() {
		t.Fatalf("got %+v", c.Job)
	}
	waitFor(t, func() bool {
		c, _ := m.Components().Find(Valkey)
		return c.Job.Phase == PhaseBuilding && c.Job.Progress == -1
	})
	if err := m.Start(Valkey); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}
	if err := m.Uninstall(Valkey); !errors.Is(err, ErrBusy) {
		t.Fatalf("uninstall during job: %v", err)
	}

	f.release <- nil
	m.Wait()
	c, _ = m.Components().Find(Valkey)
	if c.Job != nil || c.Installed != "9.1.2" || c.Update {
		t.Fatalf("got %+v", c)
	}
	if log, _ := os.ReadFile(m.LogPath(Valkey)); !strings.Contains(string(log), "compiling 9.1.2") {
		t.Fatalf("log: %s", log)
	}
	if err := m.Start(Valkey); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("up to date: %v", err)
	}

	// An update that fails stays visible until dismissed.
	f.mu.Lock()
	f.latest = "9.1.3"
	f.mu.Unlock()
	_ = m.Check(context.Background())
	if c, _ := m.Components().Find(Valkey); !c.Update || c.Latest != "9.1.3" {
		t.Fatalf("got %+v", c)
	}
	if err := m.Start(Valkey); err != nil {
		t.Fatal(err)
	}
	f.release <- errors.New("make: *** [all] Error 2")
	m.Wait()
	c, _ = m.Components().Find(Valkey)
	if c.Job == nil || c.Job.State != "failed" || c.Job.Action != "update" || !strings.Contains(c.Job.Error, "Error 2") || c.Installed != "9.1.2" {
		t.Fatalf("got %+v %+v", c, c.Job)
	}
	if err := m.Dismiss(Valkey); err != nil {
		t.Fatal(err)
	}
	if c, _ := m.Components().Find(Valkey); c.Job != nil {
		t.Fatal("dismissed job still there")
	}

	// A canceled job disappears.
	if err := m.Start(Valkey); err != nil {
		t.Fatal(err)
	}
	if err := m.Cancel(Valkey); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	if c, _ := m.Components().Find(Valkey); c.Job != nil || c.Installed != "9.1.2" {
		t.Fatalf("after cancel: %+v", c)
	}

	if err := m.Uninstall(Valkey); err != nil {
		t.Fatal(err)
	}
	if c, _ := m.Components().Find(Valkey); c.Installed != "" {
		t.Fatal("not uninstalled")
	}
	if err := m.Start("mysql"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestCheckErrorsKeepKnownVersions(t *testing.T) {
	ok := &fakeDriver{latest: "18.6"}
	broken := &fakeDriver{latest: "9.1.2"}
	root := t.TempDir()
	m := NewManager(root, []*Driver{broken.driver(Valkey), ok.driver(PostgresClient)})
	if err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}

	broken.latestErr = errors.New("rate limited")
	ok.latest = "18.7"
	if err := m.Check(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	got := m.Components()
	redis, _ := got.Find(Valkey)
	client, _ := got.Find(PostgresClient)
	if redis.Latest != "9.1.2" || client.Latest != "18.7" || !strings.Contains(got.CheckError, "rate limited") || got.CheckedAt == nil {
		t.Fatalf("got %+v", got)
	}

	// The state survives a restart.
	again := NewManager(root, []*Driver{broken.driver(Valkey), ok.driver(PostgresClient)})
	if c, _ := again.Components().Find(PostgresClient); c.Latest != "18.7" {
		t.Fatalf("got %+v", c)
	}
}

func TestStartLooksUpUnknownLatest(t *testing.T) {
	f := &fakeDriver{latest: "18.6", release: make(chan error, 1)}
	m := NewManager(t.TempDir(), []*Driver{f.driver(PostgresClient)})
	f.release <- nil
	if err := m.Start(PostgresClient); err != nil {
		t.Fatal(err)
	}
	m.Wait()
	if c, _ := m.Components().Find(PostgresClient); c.Installed != "18.6" || c.Job != nil {
		t.Fatalf("got %+v", c)
	}
	// Never checked: Components started a background check; let it finish before cleanup.
	waitFor(t, func() bool { return !m.checking.Load() })
}

func TestLogStripsColours(t *testing.T) {
	var b strings.Builder
	w := &lockedWriter{w: &b, strip: true}
	in := []byte("    \x1b[34;1mCC\x1b[0m \x1b[38:5:250mserver.o\n")
	if n, err := w.Write(in); err != nil || n != len(in) {
		t.Fatalf("%d %v", n, err)
	}
	if b.String() != "    CC server.o\n" {
		t.Fatalf("got %q", b.String())
	}
}
