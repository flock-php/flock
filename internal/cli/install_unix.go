//go:build unix

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func exeName(name string) string { return name }

func writeShims(root, bin string) error {
	dir := filepath.Join(root, "bin")
	for _, tool := range tools {
		script := fmt.Sprintf("#!/bin/sh\nexec %q exec %s \"$@\"\n", bin, tool)
		if err := os.WriteFile(filepath.Join(dir, tool), []byte(script), 0o755); err != nil {
			return err
		}
	}
	for _, alias := range cliAliases {
		link := filepath.Join(dir, alias)
		if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Symlink(filepath.Base(bin), link); err != nil {
			return err
		}
	}
	return nil
}

// installHelperBinary: only Windows has a hosts helper.
func installHelperBinary(string) error { return nil }

// errDeclined is what the app looks for to tell a cancelled password dialog from a failure.
var errDeclined = errors.New("administrator permission was declined")

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// removeAfterExit is never needed: unix deletes running programs.
func removeAfterExit(string) bool { return false }

// profilePath is where the shell profile name lives: zsh reads .zshrc from $ZDOTDIR when that is
// set, and every other profile is in home. A relative ZDOTDIR is ignored, as appDirs ignores a
// relative XDG_* dir, and so is one that is not an existing directory, so that install does not
// fail where it used to write ~/.zshrc.
func profilePath(home, name string) string {
	if dir := os.Getenv("ZDOTDIR"); name == ".zshrc" && filepath.IsAbs(dir) {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, name)
		}
	}
	return filepath.Join(home, name)
}

// profileFiles is every file removeFromShellProfiles cleans for the profiles names: each one's
// profilePath, and ~/.zshrc too when $ZDOTDIR moves .zshrc, where Flock wrote before it followed
// ZDOTDIR.
func profileFiles(home string, names []string) []string {
	var paths []string
	for _, name := range names {
		path := profilePath(home, name)
		paths = append(paths, path)
		if legacy := filepath.Join(home, name); legacy != path {
			paths = append(paths, legacy)
		}
	}
	return paths
}
