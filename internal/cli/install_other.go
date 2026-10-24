//go:build !linux

package cli

import "github.com/spf13/cobra"

// linkPackaged: only Linux has a package that updates flock in place.
func linkPackaged(string, string) (bool, error) { return false, nil }

// refreshAfterUpgrade: the macOS and Windows apps run `flock install` after an update.
func refreshAfterUpgrade(*cobra.Command, string) {}
