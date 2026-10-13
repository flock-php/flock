package cli

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/phpbuild"
)

// The shims only offer an install for these typed errors, so they must survive resolveTool.
func TestMissingVersionsAreTypedErrors(t *testing.T) {
	s := &state{root: t.TempDir(), cfg: config.Default()}

	_, _, _, err := resolveTool(s, t.TempDir(), "composer", nil)
	var phpErr *phpMissingError
	if !errors.As(err, &phpErr) || phpErr.minor != s.cfg.DefaultPHP {
		t.Fatalf("missing PHP: got %v", err)
	}
	if !strings.Contains(err.Error(), "flock php install "+s.cfg.DefaultPHP) {
		t.Fatalf("the error must say how to install it: %v", err)
	}

	_, _, _, err = resolveTool(s, t.TempDir(), "npm", nil)
	var nodeErr *node.NotInstalledError
	if !errors.As(err, &nodeErr) || nodeErr.Spec != s.cfg.DefaultNode {
		t.Fatalf("missing Node: got %v", err)
	}
}

func TestPHPQuestionSaysWhatInstallingTakes(t *testing.T) {
	ask := func(v api.PHPRelease) string {
		t.Helper()
		v.Minor = "8.5"
		q, err := phpQuestion(v)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}

	release := func(status string) api.PHPRelease {
		return api.PHPRelease{Version: phpbuild.Version{Status: status}}
	}

	if q := ask(release("ready")); !strings.Contains(q, "Install it now?") {
		t.Errorf("ready build: %q", q)
	}
	running := release("likely")
	running.Job = &phpbuild.Job{State: phpbuild.Running}
	if q := ask(running); !strings.Contains(q, "already preparing") {
		t.Errorf("running build: %q", q)
	}
	want := "Compiling PHP takes a few minutes"
	if runtime.GOOS == "windows" {
		want = "Download and install it now?"
	}
	if q := ask(release("likely")); !strings.Contains(q, want) {
		t.Errorf("no build yet: %q, want %q", q, want)
	}

	unsupported := release("unsupported")
	unsupported.Minor, unsupported.Note = "9.0", "not supported yet."
	if _, err := phpQuestion(unsupported); err == nil || !strings.Contains(err.Error(), "not supported yet.") {
		t.Errorf("unsupported versions must not prompt but explain: %v", err)
	}
}
