package cli

import (
	"os"
	"path/filepath"
	"slices"
)

// utilLinuxFlock are where distributions install util-linux's flock(1), the file-locking command
// scripts and cron jobs use. Flock's bin dir comes first in PATH, so its `flock` hands them over.
var utilLinuxFlock = []string{"/usr/bin/flock", "/bin/flock"}

// ownArgs are first arguments that are Flock's besides its commands: help and shell completion.
var ownArgs = []string{"-h", "--help", "help", "completion", "__complete", "__completeNoDesc"}

// PassThrough returns util-linux's flock when args (os.Args) are a call to it rather than to
// Flock: the program was called as `flock` and the first argument isn't one of Flock's commands,
// e.g. `flock -n /tmp/lock make` or `flock 9`. Called as `flk`, it is always Flock.
func PassThrough(args []string) (string, bool) {
	if len(args) < 2 || filepath.Base(args[0]) != "flock" || slices.Contains(ownArgs, args[1]) {
		return "", false
	}
	for _, c := range NewRootCommand().Commands() {
		if c.Name() == args[1] || slices.Contains(c.Aliases, args[1]) {
			return "", false
		}
	}
	for _, path := range utilLinuxFlock {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}
