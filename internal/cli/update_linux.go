package cli

import "errors"

// openUpdateLink: Flock has no app on Linux to update it.
func openUpdateLink() error {
	return errors.New("there is no Flock app on Linux: get the new flock-php from https://github.com/flock-php/flock/releases and run its `flock-php install`")
}
