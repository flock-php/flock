package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeRoot lays out a data dir the way Flock leaves it, plus a file Flock doesn't know.
func fakeRoot(t *testing.T) (root, cache string) {
	t.Helper()
	root, cache = t.TempDir(), t.TempDir()
	outside := outsideDirs
	outsideDirs = func() []string { return []string{cache} }
	t.Cleanup(func() { outsideDirs = outside })

	for _, f := range []string{
		"config.json", "api.json", "hosts.txt", "notes.txt",
		"bin/flock.exe", "php/8.4/bin/php.exe", "node/22.11.0/node.exe", "certs/ca.key", "log/daemon.log",
		"mail/1.eml", "tools/composer.phar",
		"services/postgres/18.6/bin/postgres.exe", "services/postgres/data/PG_VERSION",
		"services/valkey/8.1/valkey-server", "services/postgres/18.6-build1/bin/psql.exe",
	} {
		path := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, cache
}

func remaining(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func TestUninstallKeepsSettingsAndDatabases(t *testing.T) {
	root, cache := fakeRoot(t)

	kept, err := cleanUp(root, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"config.json", "notes.txt", "services/postgres/data/PG_VERSION"}
	if got := remaining(t, root); !slices.Equal(got, want) {
		t.Fatalf("left %v, want %v", got, want)
	}
	if !slices.Equal(kept, []string{filepath.Join(root, "config.json"), filepath.Join(root, "services", "postgres", "data")}) {
		t.Fatalf("reported kept %v", kept)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("the cache must be removed: %v", err)
	}
}

// Only a plain uninstall with something to keep asks whether to delete it.
func TestUninstallAsksOnlyWhenThereIsDataToKeep(t *testing.T) {
	root, _ := fakeRoot(t)
	if !hasUserData(root) {
		t.Fatal("config.json and databases are user data")
	}
	if _, err := cleanUp(root, true); err != nil {
		t.Fatal(err)
	}
	if hasUserData(root) {
		t.Fatal("after a purge there is nothing left to ask about")
	}
	// Tests have no terminal: without --purge, the data is kept.
	if decidePurge(root, false) || !decidePurge(root, true) {
		t.Fatal("without a terminal, only --purge deletes the data")
	}
}

func TestUninstallPurgeOnlyDeletesWhatFlockKnows(t *testing.T) {
	root, _ := fakeRoot(t)

	kept, err := cleanUp(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 0 {
		t.Fatalf("purge keeps nothing, got %v", kept)
	}
	// A file Flock didn't create stays, and so does the folder holding it.
	if got := remaining(t, root); !slices.Equal(got, []string{"notes.txt"}) {
		t.Fatalf("left %v", got)
	}
}
