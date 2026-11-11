//go:build !linux && !darwin && !windows

package cli

import (
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS: platforms with no DNS/CA integration (install_other.go's no-op fallbacks)
// have nothing per-suffix to retire or trust; the sweep still takes every root store
// outside the active set out of the store, so provisioning (the disk ∩ the active set)
// never sees a retired suffix's root again.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA) error {
	_, err := sweepStaleRoots(io.Discard, filepath.Join(s.root, "certs"), s.cfg.ActiveSuffixes(), sweepOpts{remove: true})
	return err
}
