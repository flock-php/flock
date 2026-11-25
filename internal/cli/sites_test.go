package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/config"
)

// link runs `flock link` in dir, as a terminal would, and returns its output.
func link(t *testing.T, root, dir string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("FLOCK_HOME", root)
	t.Chdir(dir)

	if args == nil {
		args = []string{}
	}
	cmd := newLinkCommand()
	cmd.SetArgs(args)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	return out.String(), err
}

func TestLinkNeedsANameWhenTheFolderMakesNoSiteName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(t.TempDir(), "我们的项目")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := link(t, root, dir)
	if err == nil || !strings.Contains(err.Error(), "flock link <name>") {
		t.Fatalf("err = %v; want an error that names the fix", err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 0 {
		t.Fatalf("a site was stored: %+v", cfg.Sites)
	}
}

func TestLinkRejectsNamesThatAreNotHostnames(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()

	for _, name := range []string{"", "我们的项目", "!!!"} {
		if _, err := link(t, root, dir, name); err == nil || !strings.Contains(err.Error(), "can't be a site name") {
			t.Errorf("flock link %q: err = %v", name, err)
		}
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 0 {
		t.Fatalf("a site was stored: %+v", cfg.Sites)
	}
}

func TestLinkStoresValidSiteNames(t *testing.T) {
	t.Run("from the folder name", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(t.TempDir(), "My_App")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		out, err := link(t, root, dir)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "http://my-app.test") {
			t.Errorf("output = %q; want the site's URL", out)
		}

		cfg, err := config.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Sites) != 1 || cfg.Sites[0].Name != "my-app" || cfg.Sites[0].Path != dir {
			t.Fatalf("sites = %+v", cfg.Sites)
		}
	})

	t.Run("passed name", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(t.TempDir(), "我们的项目")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		if _, err := link(t, root, dir, "my-project"); err != nil {
			t.Fatal(err)
		}

		cfg, err := config.Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Sites) != 1 || cfg.Sites[0].Name != "my-project" || cfg.Sites[0].Path != dir {
			t.Fatalf("sites = %+v", cfg.Sites)
		}
	})
}
