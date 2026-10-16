// Package mcpclients registers Flock's MCP server (`flock mcp`) with AI clients: Claude Code,
// Cursor, VS Code, Windsurf and Codex. Flock is registered once for the user (not per project), so
// it is available in every project; the server finds the project itself.
//
// Only Flock's own entry is ever touched. A config file Flock can't parse (e.g. JSON with comments)
// is left alone, and the error says so; Snippet is what to paste instead.
package mcpclients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Name is the server's name in the clients' configs.
const Name = "flock"

// States of a client.
const (
	Missing    = "missing"    // not installed on this machine
	Available  = "available"  // installed, Flock not registered
	Registered = "registered" // Flock is registered
)

// Client is one AI client and whether Flock is registered with it.
type Client struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	// Config is the file the registration lives in.
	Config string `json:"config,omitempty"`
	// Problem says why the state couldn't be read (e.g. a config file Flock can't parse).
	Problem string `json:"problem,omitempty"`
}

// ErrUnknown is returned for a client id Flock doesn't support; ErrNotInstalled for a client this
// machine doesn't have.
var (
	ErrUnknown      = errors.New("unknown client")
	ErrNotInstalled = errors.New("isn't installed on this machine")
)

// Env is where the clients live on this machine and how to run Flock.
type Env struct {
	Home      string // the user's home directory
	ConfigDir string // os.UserConfigDir: ~/Library/Application Support, %AppData%
	GOOS      string
	// Command is the flock executable the clients start (absolute); Args are its arguments.
	Command string
	Args    []string
	// LookPath finds a command (exec.LookPath, plus the usual install locations).
	LookPath func(name string) (string, error)
	// Run runs a command and returns its combined output.
	Run func(name string, args ...string) ([]byte, error)
}

// DefaultEnv is this machine's environment, running the flock binary installed in root.
func DefaultEnv(root string) (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Env{}, err
	}
	env := Env{Home: home, ConfigDir: configDir, GOOS: runtime.GOOS, Command: Executable(root), Args: []string{"mcp"},
		Run: func(name string, args ...string) ([]byte, error) { return exec.Command(name, args...).CombinedOutput() }}
	env.LookPath = func(name string) (string, error) { return lookPath(env, name) }
	return env, nil
}

// Executable is the flock binary clients should start: the one `flock install` copied into
// <root>/bin, which stays put across updates, or else the running one.
func Executable(root string) string {
	exe := "flock"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if installed := filepath.Join(root, "bin", exe); fileExists(installed) {
		return installed
	}
	if self, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			return resolved
		}
		return self
	}
	return exe
}

// lookPath finds a command on PATH, or where its installers usually put it: apps started from the
// Dock or Start menu (and the daemon) don't get the PATH a terminal has.
func lookPath(env Env, name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	var candidates []string
	if env.GOOS == "windows" {
		for _, dir := range []string{filepath.Join(env.Home, ".local", "bin"), filepath.Join(env.ConfigDir, "npm")} {
			for _, ext := range []string{".exe", ".cmd"} {
				candidates = append(candidates, filepath.Join(dir, name+ext))
			}
		}
	} else {
		candidates = []string{
			filepath.Join(env.Home, ".local", "bin", name), filepath.Join(env.Home, ".claude", "local", name),
			"/opt/homebrew/bin/" + name, "/usr/local/bin/" + name, filepath.Join(env.Home, ".npm-global", "bin", name),
		}
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}
	return "", exec.ErrNotFound
}

// client is how Flock reads and changes one client's registration.
type client struct {
	id, name string
	// installed reports whether the client is on this machine.
	installed func(Env) bool
	// config is the file the registration lives in.
	config func(Env) string
	// registered reports whether Flock is registered.
	registered func(Env) (bool, error)
	register   func(Env) error
	unregister func(Env) error
}

func clients() []client {
	return []client{claudeCode(), jsonClient("cursor", "Cursor", "mcpServers", false,
		func(e Env) string { return filepath.Join(e.Home, ".cursor", "mcp.json") },
		func(e Env) bool { return dirExists(filepath.Join(e.Home, ".cursor")) }),
		jsonClient("vscode", "VS Code", "servers", true,
			func(e Env) string { return filepath.Join(vscodeUserDir(e), "mcp.json") },
			func(e Env) bool { return dirExists(vscodeUserDir(e)) }),
		jsonClient("windsurf", "Windsurf", "mcpServers", false,
			func(e Env) string { return filepath.Join(e.Home, ".codeium", "windsurf", "mcp_config.json") },
			func(e Env) bool { return dirExists(filepath.Join(e.Home, ".codeium", "windsurf")) }),
		codex(),
	}
}

func vscodeUserDir(e Env) string {
	if e.GOOS == "linux" {
		return filepath.Join(e.Home, ".config", "Code", "User")
	}
	return filepath.Join(e.ConfigDir, "Code", "User")
}

func find(id string) (client, error) {
	for _, c := range clients() {
		if c.id == id {
			return c, nil
		}
	}
	return client{}, fmt.Errorf("%w %q (use claude-code, cursor, vscode, windsurf or codex)", ErrUnknown, id)
}

// IDs are the supported clients.
func IDs() []string {
	var ids []string
	for _, c := range clients() {
		ids = append(ids, c.id)
	}
	return ids
}

// List returns every supported client with its state.
func List(env Env) []Client {
	var out []Client
	for _, c := range clients() {
		out = append(out, state(env, c))
	}
	return out
}

// Get returns one client's state.
func Get(env Env, id string) (Client, error) {
	c, err := find(id)
	if err != nil {
		return Client{}, err
	}
	return state(env, c), nil
}

func state(env Env, c client) Client {
	out := Client{ID: c.id, Name: c.name, State: Missing, Config: c.config(env)}
	registered, err := c.registered(env)
	switch {
	case err != nil:
		out.Problem = err.Error()
		if c.installed(env) {
			out.State = Available
		}
	case registered:
		out.State = Registered
	case c.installed(env):
		out.State = Available
	}
	return out
}

// Register adds Flock to a client (replacing an older Flock entry).
func Register(env Env, id string) error {
	c, err := find(id)
	if err != nil {
		return err
	}
	if !c.installed(env) {
		return fmt.Errorf("%s %w", c.name, ErrNotInstalled)
	}
	return c.register(env)
}

// Unregister removes Flock from a client; nothing else in its config changes.
func Unregister(env Env, id string) error {
	c, err := find(id)
	if err != nil {
		return err
	}
	return c.unregister(env)
}

// Snippet is the JSON to paste into a client Flock doesn't register itself.
func Snippet(env Env) string {
	entry := map[string]any{"mcpServers": map[string]any{Name: map[string]any{"command": env.Command, "args": env.Args}}}
	data, _ := json.MarshalIndent(entry, "", "  ")
	return string(data)
}

// ---------------------------------------------------------------------------------------------
// Claude Code: `claude mcp add --scope user`, which writes ~/.claude.json. That file is large and
// Claude Code rewrites it while it runs, so Flock only edits it itself when the CLI isn't found.

func claudeCode() client {
	file := func(e Env) string { return filepath.Join(e.Home, ".claude.json") }
	cli := func(e Env) string {
		path, _ := e.LookPath("claude")
		return path
	}
	direct := jsonClient("claude-code", "Claude Code", "mcpServers", false, file, nil)

	return client{
		id: "claude-code", name: "Claude Code", config: file,
		installed:  func(e Env) bool { return cli(e) != "" || fileExists(file(e)) },
		registered: direct.registered,
		register: func(e Env) error {
			path := cli(e)
			if path == "" {
				return direct.register(e)
			}
			// Replace an entry from an older Flock (e.g. another path); a missing one is fine.
			_, _ = e.Run(path, "mcp", "remove", Name, "--scope", "user")
			args := append([]string{"mcp", "add", "--transport", "stdio", "--scope", "user", Name, "--", e.Command}, e.Args...)
			if out, err := e.Run(path, args...); err != nil {
				return fmt.Errorf("claude mcp add: %s", firstLine(out, err))
			}
			return nil
		},
		unregister: func(e Env) error {
			path := cli(e)
			if path == "" {
				return direct.unregister(e)
			}
			if registered, _ := direct.registered(e); !registered {
				return nil
			}
			if out, err := e.Run(path, "mcp", "remove", Name, "--scope", "user"); err != nil {
				return fmt.Errorf("claude mcp remove: %s", firstLine(out, err))
			}
			return nil
		},
	}
}

func firstLine(out []byte, err error) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// ---------------------------------------------------------------------------------------------
// JSON config files: {"<key>": {"flock": {"command": ..., "args": [...]}}}.

func jsonClient(id, name, key string, typed bool, file func(Env) string, installed func(Env) bool) client {
	entry := func(e Env) json.RawMessage {
		m := map[string]any{"command": e.Command, "args": e.Args}
		if typed {
			m["type"] = "stdio"
		}
		data, _ := json.Marshal(m)
		return data
	}
	return client{
		id: id, name: name, config: file, installed: installed,
		registered: func(e Env) (bool, error) {
			doc, err := readObject(file(e))
			if err != nil || doc == nil {
				return false, err
			}
			servers, err := doc.object(key)
			if err != nil || servers == nil {
				return false, err
			}
			_, ok := servers.values[Name]
			return ok, nil
		},
		register: func(e Env) error {
			return editObject(file(e), func(doc *object) error {
				servers, err := doc.object(key)
				if err != nil {
					return err
				}
				if servers == nil {
					servers = &object{}
				}
				servers.set(Name, entry(e))
				return doc.setObject(key, servers)
			})
		},
		unregister: func(e Env) error {
			if !fileExists(file(e)) {
				return nil
			}
			return editObject(file(e), func(doc *object) error {
				servers, err := doc.object(key)
				if err != nil || servers == nil {
					return err
				}
				if !servers.remove(Name) {
					return errUnchanged
				}
				return doc.setObject(key, servers)
			})
		},
	}
}

var errUnchanged = errors.New("unchanged")

// editObject applies change to the JSON object in path (created when missing) and writes it back
// atomically, keeping the order of the keys.
func editObject(path string, change func(*object) error) error {
	doc, err := readObject(path)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = &object{}
	}
	if err := change(doc); errors.Is(err, errUnchanged) {
		return nil
	} else if err != nil {
		return err
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return writeFile(path, out.Bytes())
}

// readObject reads the JSON object in path; nil when the file doesn't exist or is empty.
func readObject(path string) (*object, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var doc object
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("Flock can't read %s (%v) — add Flock to it by hand", path, err)
	}
	return &doc, nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".flock-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
