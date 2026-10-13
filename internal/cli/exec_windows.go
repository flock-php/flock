//go:build windows

package cli

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
)

// execReplace runs the tool as a child and exits with its code; Windows has no exec(2).
// Ctrl+C reaches both processes (they share the console): the tool decides what to do with it,
// and flock keeps waiting so the shell doesn't get its prompt back while the tool still runs.
func execReplace(path string, argv, env []string) error {
	signal.Ignore(os.Interrupt)

	cmd := exec.Command(path, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
