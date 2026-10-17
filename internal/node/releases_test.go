package node

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestUninstallAndRemoveOlder(t *testing.T) {
	root := t.TempDir()
	for _, v := range []string{"22.20.0", "22.21.0", "20.19.5"} {
		if err := os.MkdirAll(filepath.Join(Dir(root), v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	RemoveOlder(root, "22.21.0")
	if got, _ := Installed(root); !slices.Equal(got, []string{"22.21.0", "20.19.5"}) {
		t.Fatalf("after RemoveOlder: %v", got)
	}
	if err := Uninstall(root, "22"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Installed(root); !slices.Equal(got, []string{"20.19.5"}) {
		t.Fatalf("after Uninstall: %v", got)
	}
}
