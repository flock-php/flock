package cli

import (
	"errors"
	"os/exec"
	"strings"
)

// openUpdateLink hands flock://update to the app, which registers itself for the scheme.
// xdg-open would open some other program (or nothing) for an unknown scheme, so that is checked first.
func openUpdateLink() error {
	out, _ := exec.Command("xdg-mime", "query", "default", "x-scheme-handler/flock").Output()
	if strings.TrimSpace(string(out)) == "" {
		return errors.New("the Flock app isn't installed: download it from https://flockphp.com to get updates")
	}
	return exec.Command("xdg-open", updateLink).Run()
}
