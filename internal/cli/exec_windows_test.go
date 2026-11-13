//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flock/internal/node"
)

func fakeWindowsNode(t *testing.T, root string, updated bool) {
	t.Helper()
	version := "22.1.0"
	nodeDir := filepath.Join(root, "node", version)
	npmDir := filepath.Join(nodeDir, "node_modules", "npm")
	if updated {
		npmDir = filepath.Join(root, "node-global", "22", "node_modules", "npm")
	}
	for _, dir := range []string{
		nodeDir,
		filepath.Join(npmDir, "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "node.exe"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(npmDir, "package.json"), []byte(`{"version":"12.2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"npm-cli.js", "npx-cli.js"} {
		if err := os.WriteFile(filepath.Join(npmDir, "bin", name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveToolRunsWindowsNpmThroughNodeWithSpaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Flock Data")
	fakeWindowsNode(t, root, false)
	s := &state{root: root, cfg: withDefaults()}

	path, argv, env, err := resolveTool(s, "/x", "npm", []string{"run", "dev", "--", "--host", "0.0.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(root, "node", "22.1.0", "node.exe")
	npmCLI := filepath.Join(root, "node", "22.1.0", "node_modules", "npm", "bin", "npm-cli.js")
	if path != nodePath || !reflect.DeepEqual(argv, []string{nodePath, npmCLI, "run", "dev", "--", "--host", "0.0.0.0"}) {
		t.Fatalf("got path=%q argv=%q", path, argv)
	}
	if !strings.Contains(strings.Join(env, "\n"), "NPM_CONFIG_PREFIX="+filepath.Join(root, "node-global", "22")) {
		t.Fatalf("missing npm prefix in env: %v", env)
	}
	if !strings.Contains(strings.Join(env, "\n"), "PATH="+filepath.Join(root, "node-global", "22")+";"+filepath.Join(root, "node", "22.1.0")) {
		t.Fatalf("missing npm and Node paths in env: %v", env)
	}
}

func TestResolveToolRunsWindowsNpxThroughUpdatedNodeWithSpaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Flock Data")
	fakeWindowsNode(t, root, false)
	fakeWindowsNode(t, root, true)
	s := &state{root: root, cfg: withDefaults()}

	path, argv, _, err := resolveTool(s, "/x", "npx", []string{"vite", "--host"})
	if err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(root, "node", "22.1.0", "node.exe")
	npxCLI := filepath.Join(root, "node-global", "22", "node_modules", "npm", "bin", "npx-cli.js")
	if path != nodePath || !reflect.DeepEqual(argv, []string{nodePath, npxCLI, "vite", "--host"}) {
		t.Fatalf("got path=%q argv=%q", path, argv)
	}
	if got := node.Npx(root, "22.1.0"); got != npxCLI {
		t.Fatalf("npx script = %q, want %q", got, npxCLI)
	}
}
