//go:build !linux

package cli

import (
	"github.com/spf13/cobra"

	"flock/internal/binaries"
)

// linkPackaged: only Linux has a package that updates flock in place.
func linkPackaged(string, string) (bool, error) { return false, nil }

// refreshAfterUpgrade: the macOS and Windows apps run `flock install` after an update.
func refreshAfterUpgrade(*cobra.Command, string) {}

// followChannel: the macOS and Windows apps read the channel from the daemon.
func followChannel(*cobra.Command, binaries.Channel) error { return nil }
