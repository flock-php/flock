//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Uninstalling leaves the shell profiles as they were before `flock install`.
func TestShellProfileRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	profile, optional := filepath.Join(home, shellProfiles[0]), filepath.Join(home, shellProfiles[1])
	before := "export EDITOR=vim\nalias ll='ls -l'\n"
	if err := os.WriteFile(profile, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := addToShellPath("/f/bin", "/f/node-global/current/bin"); err != nil {
		t.Fatal(err)
	}
	// A second install with other directories rewrites the line instead of adding another.
	if err := addToShellPath("/f/bin"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(profile)
	if strings.Count(string(data), profileMarker) != 1 || !strings.Contains(string(data), `export PATH="/f/bin":"$PATH"`) {
		t.Fatalf("profile after install:\n%s", data)
	}

	removed, err := removeFromShellProfiles()
	if err != nil || !removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	data, _ = os.ReadFile(profile)
	if string(data) != before {
		t.Fatalf("profile after uninstall:\n%q\nwant\n%q", data, before)
	}
	if info, _ := os.Stat(profile); info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions changed to %v", info.Mode().Perm())
	}
	// The optional profile didn't exist, and none is created.
	if _, err := os.Stat(optional); !os.IsNotExist(err) {
		t.Fatalf("%s: %v", shellProfiles[1], err)
	}

	if removed, err := removeFromShellProfiles(); err != nil || removed {
		t.Fatalf("second removal: removed %v, err %v", removed, err)
	}
}
