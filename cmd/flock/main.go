package main

import (
	"os"
	"syscall"

	"flock/internal/cli"
)

func main() {
	// On Linux, util-linux has a flock too: calls meant for it go to it unchanged.
	if path, ok := cli.PassThrough(os.Args); ok {
		err := syscall.Exec(path, os.Args, os.Environ())
		os.Stderr.WriteString("flock: " + err.Error() + "\n")
		os.Exit(1)
	}

	// On Windows the shims (php.exe, composer.exe, ...) are links to this binary: called by one
	// of those names, it behaves like `flock exec <tool>`.
	if tool := cli.ShimTool(os.Args[0]); tool != "" {
		os.Args = append([]string{os.Args[0], "exec", tool}, os.Args[1:]...)
	}

	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
