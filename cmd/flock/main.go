package main

import (
	"os"

	"flock/internal/cli"
)

func main() {
	// On Windows the shims (php.exe, composer.exe, ...) are links to this binary: called by one
	// of those names, it behaves like `flock exec <tool>`.
	if tool := cli.ShimTool(os.Args[0]); tool != "" {
		os.Args = append([]string{os.Args[0], "exec", tool}, os.Args[1:]...)
	}

	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
