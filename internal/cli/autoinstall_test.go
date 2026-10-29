package cli

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"flock/internal/api"
	"flock/internal/node"
	"flock/internal/php"
)

// The shims only offer an install for these typed errors, so they must survive resolveTool.
func TestMissingVersionsAreTypedErrors(t *testing.T) {
	s := &state{root: t.TempDir(), cfg: withDefaults()}

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

	available := api.PHPRelease{Release: php.Release{Latest: "8.5.11", Size: 64 << 20}}
	if q := ask(available); !strings.Contains(q, "Download PHP 8.5.11 (64 MB)") {
		t.Errorf("available: %q", q)
	}
	running := available
	running.Job = &php.Job{State: "running"}
	if q := ask(running); !strings.Contains(q, "already downloading") {
		t.Errorf("running install: %q", q)
	}

	if _, err := phpQuestion(api.PHPRelease{Release: php.Release{Minor: "9.0"}}); err == nil || !strings.Contains(err.Error(), "no build of it is published") {
		t.Errorf("a version without a build must not prompt but explain: %v", err)
	}
}

func TestWildcardNoteMentionsTheHostsFileOnlyOnWindows(t *testing.T) {
	var out strings.Builder
	wildcardNote(&out, api.DNSStatus{TLD: "test", WildcardProblem: "something is off"})
	hosts := strings.Contains(out.String(), "hosts file")
	if hosts != (runtime.GOOS == "windows") || !strings.Contains(out.String(), "something is off") {
		t.Fatalf("on %s: %q", runtime.GOOS, out.String())
	}
}
