//go:build !linux && !darwin && !windows

package cli

import (
	"io"
)

// retireTLD: platforms with no Flock DNS integration have nothing per TLD to retire; the
// install steps on them are the no-op fallbacks install_other.go provides.
func retireTLD(*state, io.Writer, string) error { return nil }
