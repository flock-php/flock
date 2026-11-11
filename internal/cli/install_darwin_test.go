package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/certs"
)

// Uninstalling leaves the shell profiles as they were before `flock install`.
func TestShellProfileRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	zshrc := filepath.Join(home, ".zshrc")
	before := "export EDITOR=vim\nalias ll='ls -l'\n"
	if err := os.WriteFile(zshrc, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := addToShellPath("/f/bin", "/f/node-global/current/bin"); err != nil {
		t.Fatal(err)
	}
	// A second install with other directories rewrites the line instead of adding another.
	if err := addToShellPath("/f/bin"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(zshrc)
	if strings.Count(string(data), profileMarker) != 1 || !strings.Contains(string(data), `export PATH="/f/bin":"$PATH"`) {
		t.Fatalf("profile after install:\n%s", data)
	}

	removed, err := removeFromShellProfiles()
	if err != nil || !removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	data, _ = os.ReadFile(zshrc)
	if string(data) != before {
		t.Fatalf("profile after uninstall:\n%q\nwant\n%q", data, before)
	}
	if info, _ := os.Stat(zshrc); info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions changed to %v", info.Mode().Perm())
	}
	// No ~/.bash_profile existed, and none is created.
	if _, err := os.Stat(filepath.Join(home, ".bash_profile")); !os.IsNotExist(err) {
		t.Fatalf(".bash_profile: %v", err)
	}

	if removed, err := removeFromShellProfiles(); err != nil || removed {
		t.Fatalf("second removal: removed %v, err %v", removed, err)
	}
}

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

// macOS `flock uninstall` untrusts every Flock root, not just the global one: the loop's
// scope is suffixRootCerts' whole disk set — every certs/roots/<suffix>/ca.crt sorted, the
// global root (untrusted by its own fatal call above the loop) never in it, so every root
// is untrusted exactly once — and each certificate's `security` argv is the pinned
// untrustCAArgs list, user-level, never executed here.
func TestUninstallUntrustsEveryRoot(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "certs", "ca.crt")
	loc := filepath.Join(root, "certs", "roots", "loc", "ca.crt")
	old := filepath.Join(root, "certs", "roots", "old", "ca.crt")
	for _, c := range []struct{ dir, tld string }{
		{filepath.Dir(global), "test"}, {filepath.Dir(loc), "loc"}, {filepath.Dir(old), "old"},
	} {
		if _, err := certs.Ensure(c.dir, c.tld); err != nil {
			t.Fatal(err)
		}
	}
	keychain := "/Users/u/Library/Keychains/login.keychain-db"
	fingerprint := func(path string) string {
		sum, err := certs.FileFingerprint(path)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}

	// The loop's scope: the suffix roots sorted; the global root is not among them.
	suffixes := suffixRootCerts(root)
	if want := []string{loc, old}; !slices.Equal(suffixes, want) {
		t.Fatalf("suffixRootCerts: got %v, want %v", suffixes, want)
	}
	if slices.Contains(suffixes, global) {
		t.Fatal("the global root must stay out of the per-suffix loop: untrusted once, by its own call")
	}

	// Each cert's untrust argv, as uninstall runs it: only the domains that trust are
	// asked, and the certificate leaves the login keychain by fingerprint in any case.
	for _, tc := range []struct {
		name        string
		path        string
		admin, user bool
		want        [][]string
	}{
		{
			name:  "global root, trusted by an elevated install",
			path:  global,
			admin: true,
			want: [][]string{
				{"security", "remove-trusted-cert", "-d", global},
				{"security", "delete-certificate", "-Z", fingerprint(global), keychain},
			},
		},
		{
			name: "suffix root, trusted in the user's settings by flock site tld",
			path: loc,
			user: true,
			want: [][]string{
				{"security", "remove-trusted-cert", loc},
				{"security", "delete-certificate", "-Z", fingerprint(loc), keychain},
			},
		},
		{
			name: "suffix root nothing trusts: only the keychain cleanup runs",
			path: old,
			want: [][]string{
				{"security", "delete-certificate", "-Z", fingerprint(old), keychain},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := untrustCAArgs(tc.path, fingerprint(tc.path), tc.admin, tc.user, keychain)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d commands, want %d: %v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if !slices.Equal(got[i], tc.want[i]) {
					t.Errorf("command %d: got %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
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
