package mcpclients

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Codex reads MCP servers from [mcp_servers.<name>] tables in $CODEX_HOME/config.toml
// (~/.codex/config.toml). Flock adds and removes only its own table, as text, so the rest of the
// file keeps its formatting and comments.

func codexDir(e Env) string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(e.Home, ".codex")
}

func codex() client {
	file := func(e Env) string { return filepath.Join(codexDir(e), "config.toml") }
	return client{
		id: "codex", name: "Codex", config: file,
		installed: func(e Env) bool {
			if dirExists(codexDir(e)) {
				return true
			}
			_, err := e.LookPath("codex")
			return err == nil
		},
		registered: func(e Env) (bool, error) {
			data, err := os.ReadFile(file(e))
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			_, found := withoutTable(string(data))
			return found, nil
		},
		register: func(e Env) error {
			data, err := os.ReadFile(file(e))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			text, _ := withoutTable(string(data))
			text = strings.TrimRight(text, "\n")
			if text != "" {
				text += "\n\n"
			}
			return writeFile(file(e), []byte(text+codexTable(e)))
		},
		unregister: func(e Env) error {
			data, err := os.ReadFile(file(e))
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			text, found := withoutTable(string(data))
			if !found {
				return nil
			}
			return writeFile(file(e), []byte(strings.TrimRight(text, "\n")+"\n"))
		},
	}
}

func codexTable(e Env) string {
	args := make([]string, len(e.Args))
	for i, a := range e.Args {
		args[i] = tomlString(a)
	}
	return "[mcp_servers." + Name + "]\n" +
		"command = " + tomlString(e.Command) + "\n" +
		"args = [" + strings.Join(args, ", ") + "]\n"
}

// tomlString quotes s as a TOML basic string (the escapes match Go's for what paths contain).
func tomlString(s string) string { return strconv.Quote(s) }

// withoutTable removes Flock's [mcp_servers.flock] table and its sub-tables (e.g. .env) from a
// TOML document, and reports whether it was there.
func withoutTable(text string) (string, bool) {
	lines := strings.SplitAfter(text, "\n")
	var out []string
	found, inside := false, false
	for _, line := range lines {
		if header, ok := tableHeader(line); ok {
			inside = header == "mcp_servers."+Name || strings.HasPrefix(header, "mcp_servers."+Name+".")
			if inside {
				found = true
				// Drop the blank lines that separated the table from what came before.
				for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
					out = out[:len(out)-1]
				}
				if len(out) > 0 {
					out = append(out, "\n")
				}
			}
		}
		if !inside {
			out = append(out, line)
		}
	}
	return strings.Join(out, ""), found
}

// tableHeader returns the name of a [table] or [[array]] header line, with quotes and spaces
// around the dotted parts removed.
func tableHeader(line string) (string, bool) {
	s := strings.TrimSpace(line)
	if i := strings.Index(s, "#"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return "", false
	}
	s = strings.Trim(s, "[]")
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return strings.Join(parts, "."), true
}
