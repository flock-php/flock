package paths

import (
	"runtime"
	"testing"
)

func TestDataHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG paths are Linux-only")
	}
	t.Setenv("HOME", "/home/ada")

	t.Setenv("XDG_DATA_HOME", "")
	if got, _ := dataHome(); got != "/home/ada/.local/share/flock" {
		t.Errorf("unset: %q", got)
	}
	t.Setenv("XDG_DATA_HOME", "/data")
	if got, _ := dataHome(); got != "/data/flock" {
		t.Errorf("set: %q", got)
	}
	t.Setenv("XDG_DATA_HOME", "relative")
	if got, _ := dataHome(); got != "/home/ada/.local/share/flock" {
		t.Errorf("relative: %q", got)
	}
}

func TestRootPrefersFlockHome(t *testing.T) {
	t.Setenv("FLOCK_HOME", "/tmp/flock")
	if got, _ := Root(); got != "/tmp/flock" {
		t.Errorf("Root: %q", got)
	}
}
