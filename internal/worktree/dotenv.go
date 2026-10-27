package worktree

import (
	"os"
	"strings"
)

// Dotenv is a .env file edited in place: Set changes a key's line and keeps every other line,
// comment and blank line as it was.
type Dotenv struct {
	lines []string
}

// ReadDotenv reads the .env file at path.
func ReadDotenv(path string) (*Dotenv, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	return &Dotenv{lines: strings.Split(strings.TrimSuffix(text, "\n"), "\n")}, nil
}

// Write saves the file to path.
func (d *Dotenv) Write(path string) error {
	return os.WriteFile(path, []byte(strings.Join(d.lines, "\n")+"\n"), 0o644)
}

// line finds the last assignment of key, the one dotenv parsers use: its index, and the value
// as written (quotes included).
func (d *Dotenv) line(key string) (int, string) {
	for i := len(d.lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(d.lines[i])
		l = strings.TrimPrefix(l, "export ")
		name, value, ok := strings.Cut(l, "=")
		if ok && strings.TrimSpace(name) == key {
			return i, strings.TrimSpace(value)
		}
	}
	return -1, ""
}

// Has reports whether key is assigned.
func (d *Dotenv) Has(key string) bool {
	i, _ := d.line(key)
	return i >= 0
}

// Get returns key's value without its quotes ("" when it isn't assigned). An unquoted value
// ends at a comment.
func (d *Dotenv) Get(key string) string {
	_, raw := d.line(key)
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') {
		if end := strings.IndexByte(raw[1:], raw[0]); end >= 0 {
			value := raw[1 : end+1]
			if raw[0] == '"' {
				value = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(value)
			}
			return value
		}
	}
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// Set assigns key: its last line is replaced, keeping double quotes when it had them, or a new
// line is added at the end.
func (d *Dotenv) Set(key, value string) {
	i, raw := d.line(key)
	quoted := strings.HasPrefix(raw, `"`) || strings.ContainsAny(value, " #\"'\\") || value != strings.TrimSpace(value)
	if quoted {
		value = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
	}
	if i < 0 {
		d.lines = append(d.lines, key+"="+value)
		return
	}
	prefix := ""
	if strings.HasPrefix(strings.TrimSpace(d.lines[i]), "export ") {
		prefix = "export "
	}
	d.lines[i] = prefix + key + "=" + value
}
