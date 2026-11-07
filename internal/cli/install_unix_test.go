//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// `flk` runs the same binary as `flock`, and a second install replaces the link.
func TestWriteShimsLinksAliases(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "flock")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := writeShims(root, bin); err != nil {
			t.Fatal(err)
		}
	}
	got, err := filepath.EvalSymlinks(filepath.Join(root, "bin", "flk"))
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(bin); got != want {
		t.Fatalf("flk → %s, want %s", got, want)
	}
}

// A ZDOTDIR that does not exist is ignored, so .zshrc stays in home instead of failing the install.
func TestProfilePathIgnoresMissingZDOTDIR(t *testing.T) {
	home := t.TempDir()
	zdotdir := filepath.Join(home, "zsh")
	t.Setenv("ZDOTDIR", zdotdir)
	if got, want := profilePath(home, ".zshrc"), filepath.Join(home, ".zshrc"); got != want {
		t.Fatalf("missing ZDOTDIR: got %s, want %s", got, want)
	}
	if err := os.Mkdir(zdotdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := profilePath(home, ".zshrc"), filepath.Join(zdotdir, ".zshrc"); got != want {
		t.Fatalf("existing ZDOTDIR: got %s, want %s", got, want)
	}
}
