package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS (macOS): take the retired suffixes' roots out of the trust settings and the
// login keychain, then run the install checks for the new set. The resolver files are
// per-suffix and set-diffed: ensurePrivileged finds the set out of date (a file missing, a
// Flock-marked one stale) and re-runs privileged-setup --tlds with the whole set, which
// writes the missing files and removes the stale marker-owned ones. The roots' trust is
// user-level (see trustCA): one macOS authorization dialog per certificate, and
// ensurePrivileged covers the global root with its verify-cert check.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA, retired []string) error {
	out := cmd.OutOrStdout()
	certsDir := filepath.Join(s.root, "certs")

	for _, suffix := range retired {
		certPath := filepath.Join(certsDir, "roots", suffix, "ca.crt")
		fingerprint, err := certs.FileFingerprint(certPath)
		if err == nil {
			if err := untrustRoot(certPath, fingerprint); err != nil {
				return err // a cancelled dialog: `flock install` finishes the switch
			}
			fmt.Fprintf(out, "✓ the .%s root certificate is no longer trusted\n", suffix)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.RemoveAll(filepath.Join(certsDir, "roots", suffix)); err != nil {
			return err
		}
	}

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	if err := ensurePrivileged(cmd, s, bin, ca); err != nil {
		return err
	}

	// Trust every extra suffix's root that is not trusted yet. The store has the files:
	// syncSuffixes Ensured the set before calling this.
	for _, suffix := range s.cfg.ActiveSuffixes() {
		if suffix == s.cfg.TLD {
			continue // the global root: ensurePrivileged's verify-cert check covers it
		}
		certPath := filepath.Join(certsDir, "roots", suffix, "ca.crt")
		fingerprint, err := certs.FileFingerprint(certPath)
		if err != nil {
			continue // no root on disk for a suffix that validated: nothing to trust here
		}
		if certs.TrustedByUser(fingerprint) || certs.TrustedByAdmin(fingerprint) {
			continue
		}
		fmt.Fprintf(out, "→ trusting the .%s root (macOS asks for your password)…\n", suffix)
		if err := trustCA(certPath); err != nil {
			return fmt.Errorf("trust the .%s CA: %w", suffix, err)
		}
	}
	return nil
}

// untrustRootArgs is the pure argv layer of untrustRoot, pinned by tests: the trust
// decisions go first — the admin domain (-d) and the user's own — then the certificate
// itself leaves the login keychain by SHA-1 fingerprint, exactly what untrustCA does to the
// global root on uninstall.
func untrustRootArgs(certPath, fingerprint, keychain string) [][]string {
	return [][]string{
		{"security", "remove-trusted-cert", "-d", certPath},
		{"security", "remove-trusted-cert", certPath},
		{"security", "delete-certificate", "-Z", fingerprint, keychain},
	}
}

// untrustRoot runs untrustRootArgs. The trust removals are best-effort — a certificate
// nothing ever trusted has nothing to remove — and a cancelled authorization dialog is
// errDeclined, which the caller turns into the "run `flock install`" guidance.
func untrustRoot(certPath, fingerprint string) error {
	for _, args := range untrustRootArgs(certPath, fingerprint, loginKeychain()) {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if declined := securityError(err, out); errors.Is(declined, errDeclined) {
			return declined
		}
	}
	return nil
}
