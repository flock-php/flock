//go:build !windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const profileMarker = "# Flock (added by `flock install`)"

func exeName(name string) string { return name }

func writeShims(root, bin string) error {
	dir := filepath.Join(root, "bin")
	for _, tool := range tools {
		script := fmt.Sprintf("#!/bin/sh\nexec %q exec %s \"$@\"\n", bin, tool)
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// addToShellPath prepends dirs, in this order, in the shell profiles (the first one always, the
// others if present): a marked export line, appended once and rewritten when the directories change.
func addToShellPath(dirs ...string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	quoted := make([]string, len(dirs))
	for i, dir := range dirs {
		quoted[i] = strconv.Quote(dir)
	}
	export := fmt.Sprintf("export PATH=%s:\"$PATH\"", strings.Join(quoted, ":"))
	for _, name := range shellProfiles {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil && name != shellProfiles[0] {
			continue
		}
		lines := strings.Split(string(data), "\n")
		if i := slices.Index(lines, profileMarker); i >= 0 {
			if i+1 < len(lines) && lines[i+1] == export {
				continue
			}
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "export PATH=") {
				lines[i+1] = export
			} else {
				lines = slices.Insert(lines, i+1, export)
			}
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				return err
			}
			continue
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString("\n" + profileMarker + "\n" + export + "\n")
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// removeFromShellProfiles takes out what addToShellPath added: the marker, its export line and
// the blank line before them. It reports whether a profile had them.
func removeFromShellProfiles() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	removed := false
	for _, name := range shellProfiles {
		path := filepath.Join(home, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		i := slices.Index(lines, profileMarker)
		if i < 0 {
			continue
		}
		end := i + 1
		if end < len(lines) && strings.HasPrefix(lines[end], "export PATH=") {
			end++
		}
		if i > 0 && lines[i-1] == "" {
			i--
		}
		lines = slices.Delete(lines, i, end)
		info, err := os.Stat(path)
		if err != nil {
			return removed, err
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
			return removed, err
		}
		removed = true
	}
	return removed, nil
}
