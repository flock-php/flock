package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The command line reaches the shell of `do shell script` intact, whatever the paths contain.
func TestAdministratorScriptQuoting(t *testing.T) {
	exe := "/Users/o'brien/Library/Application Support/Flock/bin/flock"
	args := administratorScript("prompt", exe, "privileged-setup", "--ca", `/a "b" $HOME \ c`)
	if len(args) != 8 || args[7] != "prompt" {
		t.Fatalf("args %q", args)
	}
	command := strings.TrimSuffix(args[6], " 2>&1")
	out, err := exec.Command("/bin/sh", "-c", "for a in "+command+"; do printf '%s\\n' \"$a\"; done").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{exe, "privileged-setup", "--ca", `/a "b" $HOME \ c`}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("shell saw %q, want %q", got, want)
	}
}

// Uninstall removes only the Flock app, not another app that happens to be called Flock.app.
func TestInstalledAppsChecksTheBundleID(t *testing.T) {
	dir := t.TempDir()
	flock, other := filepath.Join(dir, "a", "Flock.app"), filepath.Join(dir, "b", "Flock.app")
	for app, id := range map[string]string{flock: appBundleID, other: "com.example.flock"} {
		if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
			t.Fatal(err)
		}
		plist := "<dict><key>CFBundleIdentifier</key><string>" + id + "</string></dict>"
		if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	locations := appLocations
	appLocations = func() []string { return []string{flock, other, filepath.Join(dir, "missing", "Flock.app")} }
	t.Cleanup(func() { appLocations = locations })

	if got := installedApps(); !slices.Equal(got, []string{flock}) {
		t.Fatalf("installedApps() = %v", got)
	}
}

func TestAppDirsStayInTheAppsOwnLibraryFolders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range appDirs() {
		if !strings.HasPrefix(dir, filepath.Join(home, "Library")+"/") || !strings.Contains(dir, appBundleID) {
			t.Fatalf("appDirs() has %s", dir)
		}
	}
}

// The daemon finds the same commands as a terminal: Flock's shims, then global npm packages.
func TestLaunchdPathMatchesTheShellPath(t *testing.T) {
	got := strings.Split(launchdPath("/f"), ":")
	if !slices.Equal(got[:2], []string{"/f/bin", "/f/node-global/current/bin"}) || !slices.Contains(got, "/usr/bin") {
		t.Fatalf("PATH %v", got)
	}
}
