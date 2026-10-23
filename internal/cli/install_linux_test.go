package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Uninstalling leaves the shell profiles as they were before `flock install`.
func TestShellProfileRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	bashrc := filepath.Join(home, ".bashrc")
	before := "export EDITOR=vim\nalias ll='ls -l'\n"
	if err := os.WriteFile(bashrc, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "fish"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := addToShellPath("/f/bin", "/f/node-global/current/bin"); err != nil {
		t.Fatal(err)
	}
	// A second install with other directories rewrites the line instead of adding another.
	if err := addToShellPath("/f/bin"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(bashrc)
	if strings.Count(string(data), profileMarker) != 1 || !strings.Contains(string(data), `if [ -d "/f/bin" ]; then export PATH="/f/bin":"$PATH"; fi`) {
		t.Fatalf("profile after install:\n%s", data)
	}
	for _, f := range []string{".config/fish/conf.d/flock.fish", ".config/environment.d/flock.conf"} {
		if _, err := os.Stat(filepath.Join(home, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// No ~/.zshrc existed, and none is created.
	if _, err := os.Stat(filepath.Join(home, ".zshrc")); !os.IsNotExist(err) {
		t.Fatalf(".zshrc: %v", err)
	}

	removed, err := removeFromShellProfiles()
	if err != nil || !removed {
		t.Fatalf("removed %v, err %v", removed, err)
	}
	data, _ = os.ReadFile(bashrc)
	if string(data) != before {
		t.Fatalf("profile after uninstall:\n%q\nwant\n%q", data, before)
	}
	if info, _ := os.Stat(bashrc); info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions changed to %v", info.Mode().Perm())
	}
	for _, f := range []string{".config/fish/conf.d/flock.fish", ".config/environment.d/flock.conf"} {
		if _, err := os.Stat(filepath.Join(home, f)); !os.IsNotExist(err) {
			t.Fatalf("%s after uninstall: %v", f, err)
		}
	}

	if removed, err := removeFromShellProfiles(); err != nil || removed {
		t.Fatalf("second removal: removed %v, err %v", removed, err)
	}
}

// The PATH line works in sh, and only while Flock's bin dir exists.
func TestShellPathLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, "it's here", "bin")
	if err := addToShellPath(bin); err != nil {
		t.Fatal(err)
	}
	path := func() string {
		out, err := exec.Command("sh", "-c", `PATH=/usr/bin:/bin; . "$HOME/.bashrc"; echo "$PATH"`).Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	if got := path(); got != "/usr/bin:/bin" {
		t.Fatalf("without the bin dir: %q", got)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := path(); got != bin+":/usr/bin:/bin" {
		t.Fatalf("with the bin dir: %q", got)
	}
}

// Paths with spaces, quotes, % and $ reach the daemon as they are.
func TestInstanceDropInQuoting(t *testing.T) {
	got := instanceDropIn(`/home/a "b"/50% $x`, `/home/a "b"/50% $x/bin/flock`)
	for _, want := range []string{
		`Environment="FLOCK_HOME=/home/a \"b\"/50%% $x" "PATH=/home/a \"b\"/50%% $x/bin:`,
		"ExecStart=\n",
		`ExecStart="/home/a \"b\"/50%% $$x/bin/flock" daemon --log-file "/home/a \"b\"/50%% $$x/log/daemon.log"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, got)
		}
	}
}

// Only files the privileged setup wrote are removed.
func TestRemoveManaged(t *testing.T) {
	dir := t.TempDir()
	ours, theirs, rule := filepath.Join(dir, "ours"), filepath.Join(dir, "theirs"), filepath.Join(dir, "rule")
	_ = os.WriteFile(ours, []byte(unitTemplate), 0o644)
	_ = os.WriteFile(theirs, []byte("[Unit]\n"), 0o644)
	_ = os.WriteFile(rule, []byte(polkitRuleContent), 0o644)
	for _, p := range []string{ours, theirs, rule, filepath.Join(dir, "missing")} {
		if err := removeManaged(p); err != nil {
			t.Fatal(err)
		}
	}
	if fileExists(ours) || fileExists(rule) || !fileExists(theirs) {
		t.Fatalf("ours %v, rule %v, theirs %v", fileExists(ours), fileExists(rule), fileExists(theirs))
	}
}

// The .deb and .rpm ship the unit template and polkit rule the privileged setup writes otherwise.
func TestPackagedFilesMatch(t *testing.T) {
	for file, want := range map[string]string{
		"flock@.service": unitTemplate,
		"50-flock.rules": polkitRuleContent,
	} {
		got, err := os.ReadFile(filepath.Join("..", "..", "linux", "packaging", file))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("linux/packaging/%s differs from the one in install_linux.go", file)
		}
	}
}
