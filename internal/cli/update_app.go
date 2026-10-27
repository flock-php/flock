//go:build darwin || windows

package cli

import "github.com/spf13/cobra"

// updateLink asks the app to update itself; the app's update brings the CLI and daemon along
// (it runs its bundled `flock install` after an update).
const updateLink = "flock://update"

func update(cmd *cobra.Command) error {
	if err := openUpdateLink(); err != nil {
		return err
	}
	cmd.Println("Flock's app is checking for updates.")
	return nil
}
