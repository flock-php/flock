package boost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStates(t *testing.T) {
	dir := t.TempDir()
	if State(dir) != None || Write(dir) != ErrNotBoost {
		t.Fatal("a project without Boost is left alone")
	}

	_ = os.WriteFile(filepath.Join(dir, "composer.json"), []byte(`{"require-dev": {"laravel/boost": "^2.0"}}`), 0o644)
	if State(dir) != Missing {
		t.Fatalf("requires laravel/boost: %q", State(dir))
	}
	if err := Write(dir); err != nil {
		t.Fatal(err)
	}
	if State(dir) != Current {
		t.Fatalf("after Write: %q", State(dir))
	}

	// Line endings changed by git on Windows still count as Flock's text.
	_ = os.WriteFile(File(dir), []byte(strings.ReplaceAll(Guideline, "\n", "\r\n")), 0o644)
	if State(dir) != Current {
		t.Fatal("CRLF")
	}

	saved := previous
	previous = []string{"## Flock\n\nold text\n"}
	t.Cleanup(func() { previous = saved })
	_ = os.WriteFile(File(dir), []byte(previous[0]), 0o644)
	if State(dir) != Outdated || Write(dir) != nil || State(dir) != Current {
		t.Fatal("an older guideline is replaced")
	}

	_ = os.WriteFile(File(dir), []byte("## Flock\n\nOur own notes.\n"), 0o644)
	if State(dir) != Custom || !errors.Is(Write(dir), ErrCustom) {
		t.Fatal("a hand-written flock.md is never replaced")
	}
	if data, _ := os.ReadFile(File(dir)); string(data) != "## Flock\n\nOur own notes.\n" {
		t.Fatal("changed")
	}
}

func TestUsesBoostJSON(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "boost.json"), []byte(`{}`), 0o644)
	if !Uses(dir) {
		t.Fatal("boost.json")
	}
}

func TestGuidelineIsGeneric(t *testing.T) {
	lower := strings.ToLower(Guideline)
	for _, word := range []string{"herd", "valet", "flock exec"} {
		if strings.Contains(lower, word) {
			t.Errorf("the guideline mentions %q", word)
		}
	}
}
