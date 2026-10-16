package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"flock/internal/mcpclients"
)

func TestMCPClientEndpoints(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)
	old := mcpEnv
	mcpEnv = func(string) (mcpclients.Env, error) {
		return mcpclients.Env{Home: home, ConfigDir: filepath.Join(home, "cfg"), GOOS: "darwin", Command: "/flock", Args: []string{"mcp"},
			LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}, nil
	}
	t.Cleanup(func() { mcpEnv = old })
	srv := serve(t, &fakeBackend{root: t.TempDir()}, "")

	state := func(body, id string) string {
		var m MCP
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		for _, c := range m.Clients {
			if c.ID == id {
				return c.State
			}
		}
		return ""
	}

	_, body := call(t, "GET", srv.URL+"/v1/mcp", "", "")
	if state(body, "cursor") != mcpclients.Available || state(body, "codex") != mcpclients.Missing {
		t.Fatalf("list: %s", body)
	}
	if _, body = call(t, "POST", srv.URL+"/v1/mcp/clients/cursor", "", ""); state(body, "cursor") != mcpclients.Registered {
		t.Fatalf("register: %s", body)
	}
	if _, body = call(t, "DELETE", srv.URL+"/v1/mcp/clients/cursor", "", ""); state(body, "cursor") != mcpclients.Available {
		t.Fatalf("unregister: %s", body)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/mcp/clients/zed", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown client: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/mcp/clients/codex", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing client: %d", resp.StatusCode)
	}
}

func TestBoostEndpoint(t *testing.T) {
	project := t.TempDir()
	var applied string
	old := boostApply
	boostApply = func(_ context.Context, _, dir string) (string, error) { applied = dir; return "", nil }
	t.Cleanup(func() { boostApply = old })
	srv := serve(t, &fakeBackend{root: t.TempDir(), sites: []SiteStatus{{Name: "shop", Path: project}}}, "")

	if resp, body := call(t, "POST", srv.URL+"/v1/sites/shop/boost", "", ""); resp.StatusCode != http.StatusOK || applied != project {
		t.Fatalf("%d %s (applied in %q)", resp.StatusCode, body, applied)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/sites/nope/boost", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown site: %d", resp.StatusCode)
	}
}
