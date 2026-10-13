//go:build darwin

package phpbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBuild replaces the real build script: it "builds" the version in $FAKE_VERSION,
// or fails when $FAKE_FAIL is set.
const fakeBuild = `#!/usr/bin/env bash
set -e
echo "==> Compiling PHP $1"
[[ -n "$FAKE_FAIL" ]] && { echo "boom: compiler exploded"; exit 1; }
mkdir -p "out/$FAKE_VERSION/ext"
echo php > "out/$FAKE_VERSION/php"
echo "FLOCK_BUILT $FAKE_VERSION $PWD/out/$FAKE_VERSION"
`

type fakeHooks struct {
	mu        sync.Mutex
	installed map[string]string
	installs  []string
	auto      bool
}

func (f *fakeHooks) hooks() Hooks {
	return Hooks{
		Installed: func() map[string]string {
			f.mu.Lock()
			defer f.mu.Unlock()
			out := map[string]string{}
			for k, v := range f.installed {
				out[k] = v
			}
			return out
		},
		Install: func(dir string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.installs = append(f.installs, filepath.Base(dir))
			return nil
		},
		AutoBuild: func() bool { return f.auto },
	}
}

func newTestManager(t *testing.T, hooks *fakeHooks, releases []Release, static map[string]bool) *Manager {
	t.Helper()
	old := buildScript
	buildScript = []byte(fakeBuild)
	t.Cleanup(func() { buildScript = old })

	m := NewManager(t.TempDir(), shortTempDir(t), Sources{}, hooks.hooks())
	m.state.Releases, m.state.StaticMinors = releases, static
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go m.worker(ctx)
	return m
}

// shortTempDir avoids t.TempDir under /var/folders/... (fine) but documents the no-spaces rule.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "flockbuild")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestRefusesWorkspaceWithSpaces(t *testing.T) {
	hooks := &fakeHooks{installed: map[string]string{}}
	m := newTestManager(t, hooks, []Release{{"8.3", "8.3.30", true}}, map[string]bool{"8.3": true})
	m.workspace = filepath.Join(t.TempDir(), "Application Support")

	m.Build("8.3")
	waitJob(t, m, "8.3", Failed)
	if v := status(m, "8.3"); v.Job == nil || !strings.Contains(v.Job.Error, "spaces") {
		t.Fatalf("got %+v", v.Job)
	}
}

func waitJob(t *testing.T, m *Manager, minor string, want JobState) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		m.mu.Lock()
		job := m.jobs[minor]
		state := JobState("")
		if job != nil {
			state = job.State
		}
		m.mu.Unlock()
		if state == want {
			return
		}
	}
	t.Fatalf("job %s did not reach %s", minor, want)
}

func status(m *Manager, minor string) Version {
	for _, v := range m.Versions() {
		if v.Minor == minor {
			return v
		}
	}
	return Version{}
}

func TestVersionStatuses(t *testing.T) {
	hooks := &fakeHooks{installed: map[string]string{"8.5": "8.5.11", "8.4": "8.4.25"}}
	m := newTestManager(t, hooks,
		[]Release{{"8.6", "8.6.0", true}, {"8.5", "8.5.11", true}, {"8.4", "8.4.26", true}, {"8.3", "8.3.30", true}, {"7.4", "7.4.33", false}},
		map[string]bool{"8.5": true, "8.4": true})
	m.state.Results["8.3.30"] = "failed"

	for minor, want := range map[string]string{"8.6": "unsupported", "8.5": "installed", "8.4": "update", "8.3": "failed", "7.4": "unsupported"} {
		if got := status(m, minor).Status; got != want {
			t.Errorf("%s: got %s, want %s", minor, got, want)
		}
	}
	if note := status(m, "7.4").Note; note == "" {
		t.Error("unsupported old versions need an explanation")
	}

	delete(m.state.Results, "8.3.30")
	if got := status(m, "8.3").Status; got != "unsupported" {
		t.Errorf("8.3 is older than every static-php-cli build here: got %s", got)
	}
	m.state.StaticMinors["8.2"] = true
	if got := status(m, "8.3").Status; got != "likely" {
		t.Errorf("8.3 lies between built minors: got %s", got)
	}
}

func TestInstallBuildsThenInstallsAndUpdatesAreInstantWhenReady(t *testing.T) {
	t.Setenv("FAKE_VERSION", "8.4.26")
	hooks := &fakeHooks{installed: map[string]string{"8.4": "8.4.25"}, auto: true}
	m := newTestManager(t, hooks, []Release{{"8.4", "8.4.26", true}}, map[string]bool{"8.4": true})

	// Background build of the patch update: built, not installed.
	m.scheduleAuto()
	waitJob(t, m, "8.4", Succeeded)
	if v := status(m, "8.4"); v.Status != "update" || !v.UpdateReady || len(hooks.installs) != 0 {
		t.Fatalf("after background build: %+v, installs %v", v, hooks.installs)
	}

	// The user clicks Update: installed immediately from the ready build.
	if err := m.Install("8.4"); err != nil {
		t.Fatal(err)
	}
	if len(hooks.installs) != 1 || hooks.installs[0] != "8.4.26" {
		t.Fatalf("installs: %v", hooks.installs)
	}
	if _, err := os.Stat(m.readyDir("8.4.26")); !os.IsNotExist(err) {
		t.Fatal("ready build should be removed after installing it")
	}
}

func TestFailedBuildsAreReportedWithTheLogLine(t *testing.T) {
	t.Setenv("FAKE_FAIL", "1")
	m := newTestManager(t, &fakeHooks{installed: map[string]string{}}, []Release{{"8.3", "8.3.30", true}}, map[string]bool{"8.3": true})

	if err := m.Install("8.3"); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m, "8.3", Failed)

	v := status(m, "8.3")
	if v.Status != "failed" || v.Job == nil || v.Job.Error == "" {
		t.Fatalf("got %+v", v)
	}
	if data, _ := os.ReadFile(m.LogPath("8.3")); len(data) == 0 {
		t.Fatal("build log missing")
	}
}

func TestAutoBuildsNewVersionsOnlyWhenEnabled(t *testing.T) {
	t.Setenv("FAKE_VERSION", "8.6.0")
	hooks := &fakeHooks{installed: map[string]string{"8.5": "8.5.11"}}
	m := newTestManager(t, hooks, []Release{{"8.6", "8.6.0", true}, {"8.5", "8.5.11", true}, {"8.2", "8.2.30", true}},
		map[string]bool{"8.6": true, "8.5": true})

	if m.hooks.AutoBuild() {
		t.Fatal("precondition")
	}
	hooks.auto = true
	m.scheduleAuto()
	waitJob(t, m, "8.6", Succeeded)

	if v := status(m, "8.6"); v.Status != "ready" {
		t.Fatalf("8.6 should be ready: %+v", v)
	}
	m.mu.Lock()
	_, olderQueued := m.jobs["8.2"]
	m.mu.Unlock()
	if olderQueued {
		t.Fatal("older versions must not be built automatically")
	}
}
