//go:build !linux && !darwin && !windows

package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS: platforms with no DNS/CA integration (install_other.go's no-op fallbacks)
// have nothing per-suffix to retire or trust; the roots' files still leave the store.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA, retired []string) error {
	return deleteRootDirs(filepath.Join(s.root, "certs"), retired)
}
