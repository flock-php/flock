package cli

import (
	"errors"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// update upgrades the flockphp package with the system's package manager, as the system's own
// updates do (Linux apps don't update themselves): the package's post-install script restarts
// every user's daemon, which then runs the new flock through the link in their data dir.
func update(cmd *cobra.Command) error {
	if !packaged() {
		return errors.New("this Flock isn't installed from its package: install the flockphp package from https://flockphp.com to get updates")
	}
	var steps [][]string
	switch {
	case hasCommand("apt-get"):
		steps = [][]string{{"apt-get", "update"}, {"apt-get", "install", "--only-upgrade", "flockphp"}}
	case hasCommand("dnf"):
		steps = [][]string{{"dnf", "upgrade", "--refresh", "flockphp"}}
	case hasCommand("pacman"):
		// The AUR's flockphp-bin: AUR helpers build it as the user, never with sudo.
		return errors.New("Flock comes from the AUR here: update flockphp-bin with your AUR helper (e.g. `yay -Syu` or `paru -Syu`)")
	default:
		return errors.New("neither apt nor dnf is here: update the flockphp package with your package manager")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("run `flock update` in a terminal: the package manager asks for your password")
	}
	for _, step := range steps {
		sudo := exec.Command("sudo", step...)
		sudo.Stdin, sudo.Stdout, sudo.Stderr = os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr()
		if err := sudo.Run(); err != nil {
			return err
		}
	}
	return nil
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
