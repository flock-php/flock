package mcpclients

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	return Env{
		Home: home, ConfigDir: filepath.Join(home, "config"), GOOS: "darwin",
		Command: "/Users/ann/Library/Application Support/Flock/bin/flock", Args: []string{"mcp"},
		LookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		Run:      func(string, ...string) ([]byte, error) { t.Fatal("no command should run"); return nil, nil },
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func stateOf(t *testing.T, env Env, id string) string {
	t.Helper()
	c, err := Get(env, id)
	if err != nil {
		t.Fatal(err)
	}
	return c.State
}

func TestMissingClients(t *testing.T) {
	env := testEnv(t)
	for _, c := range List(env) {
		if c.State != Missing {
			t.Errorf("%s: %s", c.ID, c.State)
		}
	}
	if err := Register(env, "cursor"); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("registering with a missing client: %v", err)
	}
	if _, err := Get(env, "zed"); err == nil {
		t.Fatal("unknown client")
	}
}

func TestCursorKeepsOtherServersAndKeyOrder(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.Home, ".cursor", "mcp.json")
	write(t, path, `{"theme": 1, "mcpServers": {"github": {"command": "gh-mcp"}, "flock": {"command": "/old/flock"}}, "zzz": true}`)

	if stateOf(t, env, "cursor") != Registered {
		t.Fatal("an existing flock entry counts as registered")
	}
	if err := Register(env, "cursor"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, `"command": "/Users/ann/Library/Application Support/Flock/bin/flock"`) || strings.Contains(got, "/old/flock") {
		t.Fatalf("the old entry is replaced:\n%s", got)
	}
	theme, github, zzz := strings.Index(got, `"theme"`), strings.Index(got, `"github"`), strings.Index(got, `"zzz"`)
	if theme < 0 || github < theme || zzz < github || strings.Index(got, `"flock"`) < github {
		t.Fatalf("key order changed:\n%s", got)
	}

	if err := Unregister(env, "cursor"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); strings.Contains(got, "flock") || !strings.Contains(got, `"gh-mcp"`) {
		t.Fatalf("after unregister:\n%s", got)
	}
	if stateOf(t, env, "cursor") != Available {
		t.Fatal("cursor is still installed")
	}
}

func TestVSCodeCreatesItsUserFileWithType(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.ConfigDir, "Code", "User")
	_ = os.MkdirAll(dir, 0o755)
	if stateOf(t, env, "vscode") != Available {
		t.Fatal("VS Code is installed")
	}
	if err := Register(env, "vscode"); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(dir, "mcp.json"))
	if !strings.Contains(got, `"servers": {`) || !strings.Contains(got, `"type": "stdio"`) || !strings.Contains(got, `"args": [`) {
		t.Fatalf("%s", got)
	}
	if stateOf(t, env, "vscode") != Registered {
		t.Fatal("registered")
	}
}

func TestUnreadableJSONIsLeftAlone(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.ConfigDir, "Code", "User", "mcp.json")
	original := "{\n  // my servers\n  \"servers\": {}\n}\n"
	write(t, path, original)

	c, _ := Get(env, "vscode")
	if c.State != Available || !strings.Contains(c.Problem, "by hand") {
		t.Fatalf("%+v", c)
	}
	if err := Register(env, "vscode"); err == nil {
		t.Fatal("a file with comments must not be rewritten")
	}
	if read(t, path) != original {
		t.Fatal("the file changed")
	}
}

func TestCodexTable(t *testing.T) {
	env := testEnv(t)
	env.Command = `C:\Users\ann\AppData\Roaming\Flock\bin\flock.exe`
	path := filepath.Join(env.Home, ".codex", "config.toml")
	write(t, path, "model = \"gpt-5\" # mine\n\n[mcp_servers.flock]\ncommand = \"/old\"\n\n[mcp_servers.flock.env]\nX = \"1\"\n\n[mcp_servers.github]\ncommand = \"gh\"\n")

	if stateOf(t, env, "codex") != Registered {
		t.Fatal("registered")
	}
	if err := Register(env, "codex"); err != nil {
		t.Fatal(err)
	}
	want := "model = \"gpt-5\" # mine\n\n[mcp_servers.github]\ncommand = \"gh\"\n\n" +
		"[mcp_servers.flock]\ncommand = \"C:\\\\Users\\\\ann\\\\AppData\\\\Roaming\\\\Flock\\\\bin\\\\flock.exe\"\nargs = [\"mcp\"]\n"
	if got := read(t, path); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	if err := Unregister(env, "codex"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "model = \"gpt-5\" # mine\n\n[mcp_servers.github]\ncommand = \"gh\"\n" {
		t.Fatalf("after unregister:\n%q", got)
	}
}

func TestClaudeCodeUsesItsCLI(t *testing.T) {
	env := testEnv(t)
	var ran []string
	env.LookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	env.Run = func(name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	if stateOf(t, env, "claude-code") != Available {
		t.Fatal("claude is on PATH")
	}
	if err := Register(env, "claude-code"); err != nil {
		t.Fatal(err)
	}
	want := "/bin/claude mcp add --transport stdio --scope user flock -- /Users/ann/Library/Application Support/Flock/bin/flock mcp"
	if len(ran) != 2 || ran[1] != want {
		t.Fatalf("ran %q", ran)
	}

	write(t, filepath.Join(env.Home, ".claude.json"), `{"mcpServers": {"flock": {"command": "x"}}, "projects": {}}`)
	if stateOf(t, env, "claude-code") != Registered {
		t.Fatal("read from ~/.claude.json")
	}
}

func TestClaudeCodeWithoutItsCLIEditsTheFile(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.Home, ".claude.json")
	write(t, path, `{"numStartups": 3, "projects": {"/x": {"mcpServers": {}}}}`)
	if err := Register(env, "claude-code"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, `"numStartups": 3`) || !strings.Contains(got, `"mcpServers": {`+"\n"+`    "flock"`) {
		t.Fatalf("%s", got)
	}
}

func TestSnippet(t *testing.T) {
	got := Snippet(testEnv(t))
	if !strings.Contains(got, `"mcpServers"`) || !strings.Contains(got, `"args": [`) {
		t.Fatal(got)
	}
}
