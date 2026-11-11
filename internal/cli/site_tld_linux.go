package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS (Linux): the system side is one file per user's whole suffix set — the
// resolved drop-in's Domains= line and the trust store's anchor copies — so ensurePrivileged
// re-runs privileged-setup with the new set and the retired suffixes' routing and anchors
// leave with the same wholesale write. What is per-suffix is in the user's reach: the
// browsers' NSS databases (certutil, no password) get every root of the set, and the
// retired roots' files and entries go before the directories do.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA, retired []string) error {
	out := cmd.OutOrStdout()
	certsDir := filepath.Join(s.root, "certs")

	for _, suffix := range retired {
		certPath := filepath.Join(certsDir, "roots", suffix, "ca.crt")
		if fileExists(certPath) {
			if err := certs.UntrustInBrowsers(certPath); err != nil {
				fmt.Fprintf(out, "! could not remove the .%s root from your browsers: %v\n", suffix, err)
			}
		}
		if err := os.RemoveAll(filepath.Join(certsDir, "roots", suffix)); err != nil {
			return err
		}
	}

	// One privileged run for the whole set: the drop-in and the anchor copies (global root
	// plus every roots/<suffix>/ca.crt on disk — Ensure ran before this), and the stale
	// anchors of suffixes that left the set.
	if err := ensurePrivileged(cmd, s, "", ca); err != nil {
		return err
	}

	// The browsers' own databases are the user's: no password. The Firefox enterprise
	// policy keeps carrying the global root (one file per user in the certs package's
	// layout); extra roots reach Firefox through its database, the ones it has.
	for _, suffix := range s.cfg.ActiveSuffixes() {
		if suffix == s.cfg.TLD {
			continue // the global root: ensurePrivileged's anchors and policy cover it
		}
		certPath := filepath.Join(certsDir, "roots", suffix, "ca.crt")
		if !fileExists(certPath) || certs.TrustedInBrowsers(certPath) {
			continue
		}
		if err := certs.TrustInBrowsers(certPath); err != nil {
			fmt.Fprintf(out, "! Firefox and Chromium browsers don't trust the .%s root: %v\n", suffix, err)
		}
	}
	return nil
}
