package dns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverProblem(t *testing.T) {
	old := ResolverDir
	ResolverDir = t.TempDir()
	t.Cleanup(func() { ResolverDir = old })

	if p := ResolverProblem("test", 53535); !strings.Contains(p, "missing") || !strings.Contains(p, "flock install") {
		t.Fatalf("missing file: %q", p)
	}

	if err := os.WriteFile(filepath.Join(ResolverDir, "test"), []byte(ResolverContent(53535)), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ResolverProblem("test", 53535); p != "" {
		t.Fatalf("current file: %q", p)
	}
	if p := ResolverProblem("test", 5300); !strings.Contains(p, "port 5300") || !strings.Contains(p, "flock install") {
		t.Fatalf("other port: %q", p)
	}
	if got := ResolverContent(53535); got != "# Managed by Flock\nnameserver 127.0.0.1\nport 53535\n" {
		t.Fatalf("content changed (must match what flock install writes): %q", got)
	}
}
