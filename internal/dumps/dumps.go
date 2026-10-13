// Package dumps captures dump()/dd() output from PHP apps without touching the apps.
//
// With VAR_DUMPER_FORMAT=server in the environment, Laravel skips its own dumper and
// Symfony's VarDumper sends every dump to a TCP dump server instead of the page. Flock
// runs that server: a small PHP script with its own copy of symfony/var-dumper (embedded
// in the flock binary), which renders each dump and appends it to a JSON-lines file.
package dumps

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"flock/internal/supervisor"
)

//go:embed all:server
var serverFiles embed.FS

// MaxEntries caps the stored history; older dumps are dropped.
const MaxEntries = 500

// Entry is one captured dump.
type Entry struct {
	ID          string    `json:"id"`
	Time        time.Time `json:"time"`
	Label       *string   `json:"label"`
	Site        string    `json:"site"`
	Source      *Source   `json:"source"`
	Request     *Request  `json:"request"`
	CommandLine *string   `json:"command_line"`
	Text        string    `json:"text"`
	HTML        string    `json:"html"`
}

// Source is where dump() was called.
type Source struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Name string `json:"name"`
}

// Request is the HTTP request that produced a dump.
type Request struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}

// File is the JSON-lines file dumps are appended to.
func File(root string) string { return filepath.Join(root, "dumps", "dumps.jsonl") }

// Env makes PHP send dumps to the server on port.
func Env(port int) []string {
	return []string{"VAR_DUMPER_FORMAT=server", fmt.Sprintf("VAR_DUMPER_SERVER=127.0.0.1:%d", port)}
}

// Extract writes the embedded server to <root>/tools/dump-server, replacing it only when it changed.
func Extract(root string) (string, error) {
	dir := filepath.Join(root, "tools", "dump-server")
	sum := checksum()
	marker := filepath.Join(dir, ".checksum")

	if data, err := os.ReadFile(marker); err == nil && string(data) == sum {
		return dir, nil
	}

	staging, err := os.MkdirTemp(filepath.Join(root, "tools"), ".dump-server-")
	if err != nil {
		if err := os.MkdirAll(filepath.Join(root, "tools"), 0o755); err != nil {
			return "", err
		}
		if staging, err = os.MkdirTemp(filepath.Join(root, "tools"), ".dump-server-"); err != nil {
			return "", err
		}
	}
	defer os.RemoveAll(staging)

	err = fs.WalkDir(serverFiles, "server", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("server", path)
		target := filepath.Join(staging, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := serverFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, ".checksum"), []byte(sum), 0o644); err != nil {
		return "", err
	}

	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	return dir, os.Rename(staging, dir)
}

func checksum() string {
	h := sha256.New()
	_ = fs.WalkDir(serverFiles, "server", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := serverFiles.ReadFile(path)
			h.Write([]byte(path))
			h.Write(data)
		}
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))
}

// Spec runs the dump server with phpBinary. -n keeps it independent of user php.ini (and Xdebug).
func Spec(root, phpBinary string, port int) (supervisor.Spec, error) {
	dir, err := Extract(root)
	if err != nil {
		return supervisor.Spec{}, err
	}
	if err := os.MkdirAll(filepath.Dir(File(root)), 0o700); err != nil {
		return supervisor.Spec{}, err
	}

	return supervisor.Spec{
		Name:    "dump-server",
		Path:    phpBinary,
		Args:    []string{"-n", "-d", "memory_limit=1G", filepath.Join(dir, "server.php"), fmt.Sprintf("127.0.0.1:%d", port), File(root)},
		LogFile: filepath.Join(root, "log", "dump-server.log"),
	}, nil
}

// List returns stored dumps, newest first. It also trims the file to MaxEntries.
func List(root string) ([]Entry, error) {
	data, err := os.ReadFile(File(root))
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}

	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) > MaxEntries {
		lines = lines[len(lines)-MaxEntries:]
		_ = os.WriteFile(File(root), append(bytes.Join(lines, []byte("\n")), '\n'), 0o600)
	}

	entries := make([]Entry, 0, len(lines))
	for i := len(lines) - 1; i >= 0; i-- {
		var e Entry
		if len(lines[i]) == 0 || json.Unmarshal(lines[i], &e) != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// Count returns how many dumps are stored, without decoding them.
func Count(root string) int {
	f, err := os.Open(File(root))
	if err != nil {
		return 0
	}
	defer f.Close()

	n := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			n++
		}
	}
	return min(n, MaxEntries)
}

// Clear deletes all stored dumps.
func Clear(root string) error {
	if err := os.Remove(File(root)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
