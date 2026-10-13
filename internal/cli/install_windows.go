package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// Windows support (hosts file via a helper service, cert store trust, .exe shims) is not built yet.
var errWindowsTODO = errors.New("not supported on Windows yet")

func exeName(name string) string { return name + ".exe" }

func writeShims(string, string) error     { return errWindowsTODO }
func addToShellPath(string) error         { return errWindowsTODO }
func installService(string, string) error { return errWindowsTODO }
func uninstallService() error             { return errWindowsTODO }
func restartService() error               { return errWindowsTODO }
func newPrivilegedSetupCommand() *cobra.Command {
	return &cobra.Command{Use: "privileged-setup", Hidden: true}
}
func newUninstallCommand() *cobra.Command {
	return &cobra.Command{Use: "uninstall", RunE: func(*cobra.Command, []string) error { return errWindowsTODO }}
}

func ensurePrivileged(*cobra.Command, *state, string, *certs.CA) error { return errWindowsTODO }
