package project

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flock/internal/config"
)

func TestRootDetectsTheConventionalFolders(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"public/index.php"}, "public"},   // Laravel, Symfony
		{[]string{"www/index.php"}, "www"},         // Nette
		{[]string{"web/index.php"}, "web"},         // Yii2, Drupal, Craft
		{[]string{"webroot/index.php"}, "webroot"}, // CakePHP
		{[]string{"public/index.php", "www/index.php"}, "public"},
		{[]string{"public/app.css", "www/index.php"}, "www"}, // a folder counts only with an index.php
		{[]string{"index.php"}, "."},
		{[]string{"index.html"}, "."},
	} {
		dir := t.TempDir()
		for _, f := range tc.files {
			write(t, filepath.Join(dir, f), "<?php\n")
		}
		r := RootFor(&config.Site{Path: dir})
		if r.Rel != tc.want || r.Dir != filepath.Join(dir, tc.want) || r.Source != Default || r.State != "" {
			t.Errorf("%v: got %+v, want %s", tc.files, r, tc.want)
		}
	}
}

func TestRootNotFoundOnlyForComposerProjectsWithoutIndex(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "composer.json"), "{}")
	write(t, filepath.Join(dir, "src", "www", "index.php"), "<?php\n")
	if r := RootFor(&config.Site{Path: dir}); r.Rel != "." || r.State != RootNotFound {
		t.Fatalf("Composer project without a root: %+v", r)
	}

	write(t, filepath.Join(dir, "index.php"), "<?php\n")
	if r := RootFor(&config.Site{Path: dir}); r.State != "" {
		t.Fatalf("Composer project serving its own folder: %+v", r)
	}

	plain := t.TempDir()
	write(t, filepath.Join(plain, "README.md"), "not PHP")
	if r := RootFor(&config.Site{Path: plain}); r.Rel != "." || r.State != RootEmpty {
		t.Fatalf("a folder that isn't a Composer project, with nothing to serve: %+v", r)
	}
}

func TestRootSetForTheSiteNeedsAnIndex(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "src", "www", "favicon.ico"), "")
	site := &config.Site{Path: dir, Root: "src/www"}
	if r := RootFor(site); r.Source != Isolated || r.State != RootEmpty {
		t.Fatalf("a root without index.php or index.html: %+v", r)
	}
	write(t, filepath.Join(dir, "src", "www", "index.html"), "<p>static</p>")
	if r := RootFor(site); r.State != "" {
		t.Fatalf("a static index.html is enough: %+v", r)
	}
}

func TestRootFilesWatchTheRootInUse(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "www"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := RootFiles(&config.Site{Path: dir, Root: "src/www"})
	for _, want := range []string{"composer.json", "index.php", "public/index.php", "src/www/index.php", "src/www/index.html"} {
		if !slices.Contains(files, filepath.Join(dir, filepath.FromSlash(want))) {
			t.Errorf("%s isn't watched: %v", want, files)
		}
	}
	if files := RootFiles(&config.Site{Path: dir, Root: "gone"}); slices.Contains(files, "index.php") {
		t.Errorf("a root that doesn't exist must not add relative paths: %v", files)
	}
}

func TestRootFollowsMachineThenFlockrcThenDetection(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "public", "index.php"), "<?php\n")
	write(t, filepath.Join(dir, "src", "www", "index.php"), "<?php\n")
	write(t, filepath.Join(dir, "admin", "index.php"), "<?php\n")
	site := &config.Site{Path: dir}

	write(t, filepath.Join(dir, RC), "php=8.4\nroot = ./src/www/\n")
	if r := RootFor(site); r.Rel != "src/www" || r.Source != Project || r.Pinned != "./src/www/" || r.File != filepath.Join(dir, RC) || r.State != "" {
		t.Fatalf(".flockrc root: %+v", r)
	}

	site.Root = "admin"
	if r := RootFor(site); r.Rel != "admin" || r.Dir != filepath.Join(dir, "admin") || r.Source != Isolated || r.Pinned == "" {
		t.Fatalf("this machine's root wins, the pin stays visible: %+v", r)
	}

	site.Root = "gone"
	if r := RootFor(site); r.State != RootMissing || r.Source != Isolated {
		t.Fatalf("a root that doesn't exist: %+v", r)
	}
	site.Root = "../elsewhere"
	if r := RootFor(site); r.State != RootMissing {
		t.Fatalf("a root outside the project: %+v", r)
	}
}

func TestRootIsNotInheritedFromParentFolders(t *testing.T) {
	parked := t.TempDir()
	write(t, filepath.Join(parked, RC), "root=www\n")
	app := filepath.Join(parked, "app")
	write(t, filepath.Join(app, "www", "index.php"), "<?php\n")
	write(t, filepath.Join(app, "public", "index.php"), "<?php\n")
	if r := RootFor(&config.Site{Path: app}); r.Rel != "public" || r.Source != Default || r.Pinned != "" {
		t.Fatalf("a parent's .flockrc must not move the root: %+v", r)
	}
}

func TestCleanRoot(t *testing.T) {
	for in, want := range map[string]string{"src/www": "src/www", "./src/www/": "src/www", ".": ".", "a/../b": "b"} {
		if got, err := CleanRoot(in); err != nil || got != want {
			t.Errorf("CleanRoot(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "  ", "..", "../x", "a/../../x", "/etc", `\x`} {
		if _, err := CleanRoot(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("CleanRoot(%q): want ErrInvalid, got %v", in, err)
		}
	}
}

func TestRootFromPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "www"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "file.txt"), "")
	if got, err := RootFromPath(dir, "src/www"); err != nil || got != "src/www" {
		t.Errorf("relative: %q %v", got, err)
	}
	if got, err := RootFromPath(dir, filepath.Join(dir, "src", "www")); err != nil || got != "src/www" {
		t.Errorf("absolute inside: %q %v", got, err)
	}
	for _, in := range []string{filepath.Dir(dir), "missing", "file.txt"} {
		if _, err := RootFromPath(dir, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", in, err)
		}
	}
}

func TestSuggestRoots(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{
		"src/www/index.php", "admin/index.php", "a/b/c/index.php", "a/b/c/d/index.php",
		"vendor/pkg/index.php", "node_modules/x/index.php", ".git/index.php", "tests/fixtures/index.php", "index.php",
	} {
		write(t, filepath.Join(dir, f), "<?php\n")
	}
	if got, want := SuggestRoots(dir), []string{"admin", "src/www", "a/b/c"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v (shallowest first, three levels, no dependencies or hidden folders)", got, want)
	}

	many := t.TempDir()
	for _, name := range strings.Split("a b c d e f g", " ") {
		write(t, filepath.Join(many, name, "index.php"), "<?php\n")
	}
	if got := SuggestRoots(many); len(got) != maxSuggestions || got[0] != "a" {
		t.Fatalf("at most %d: %v", maxSuggestions, got)
	}
}

func TestPinSiteRootLiftsTheMachineRootAndUnpinRemovesIt(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, RC), "php=8.4\n")
	write(t, filepath.Join(dir, "src", "www", "index.php"), "<?php\n")
	site := &config.Site{Path: dir, Root: "src/www"}

	file, lifted, err := PinSiteRoot(site, "")
	if err != nil || !lifted || site.Root != "" || file != filepath.Join(dir, RC) {
		t.Fatalf("pin the root in use: %q %v %v %+v", file, lifted, err, site)
	}
	if got := read(t, file); got != "php=8.4\nroot=src/www\n" {
		t.Fatalf(".flockrc keeps its other settings: %q", got)
	}
	if r := RootFor(site); r.Source != Project || r.Rel != "src/www" {
		t.Fatalf("after pinning: %+v", r)
	}

	if _, _, err := PinSiteRoot(site, "../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a root outside the project: %v", err)
	}

	changed, err := Unpin(dir, Root)
	if err != nil || len(changed) != 1 {
		t.Fatalf("unpin: %v %v", changed, err)
	}
	if got := read(t, file); got != "php=8.4\n" {
		t.Fatalf("unpin removes only root=: %q", got)
	}
}
