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
