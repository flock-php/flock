package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS (macOS): converge the roots as a set (provisionRootsOS: sweep the stores
// outside the active set out of the trust settings and the login keychain, ensure the
// active roots and their trust) and run the install checks for the set. The resolver
// files are per-suffix and set-diffed: ensurePrivileged finds the set out of date (a file
// missing, a Flock-marked one stale) and re-runs privileged-setup --tlds with the whole
// set, which writes the missing files and removes the stale marker-owned ones. The roots'
// trust is user-level (see trustCA): one macOS authorization dialog per certificate, and
// ensurePrivileged covers the global root with its verify-cert check.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	return ensurePrivileged(cmd, s, bin, ca)
}

// provisionRootsOS is the whole-set roots pass `flock install` (install_darwin.go's
// ensurePrivileged) and the provisioning sync share (F2): create-if-missing for every
// active root (the daemon reload does this when it runs; doing it here covers the
// daemon-less case), the sweep retiring every store outside the set — fingerprint,
// untrustRoot, delete — and the trust ensured for every root of the set. The hooks are
// package-level so tests drive the wiring without touching the real keychain.
func provisionRootsOS(s *state, out io.Writer) error {
	certsDir := filepath.Join(s.root, "certs")
	active := s.cfg.ActiveSuffixes()
	if err := certs.OpenRoots(certsDir, s.cfg.TLD).Ensure(active); err != nil {
		return err
	}

	swept, err := sweepStaleRoots(out, certsDir, s.cfg.TLD, active, sweepOpts{untrust: untrustRootHook, remove: true})
	if err != nil {
		return err // a cancelled dialog: `flock install` finishes the switch
	}
	for _, root := range swept {
		if root.fingerprint != "" {
			fmt.Fprintf(out, "✓ the .%s root certificate is no longer trusted\n", root.suffix)
		}
	}

	// Trust every extra suffix's root that is not trusted yet. Ensure ran above, so the
	// active set's files are on disk.
	for _, suffix := range active {
		if suffix == s.cfg.TLD {
			continue // the global root: ensurePrivileged's verify-cert check covers it
		}
		certPath := filepath.Join(certsDir, "roots", suffix, "ca.crt")
		fingerprint, err := certs.FileFingerprint(certPath)
		if err != nil {
			continue // no root on disk for a suffix that validated: nothing to trust here
		}
		if rootsTrusted(fingerprint) {
			continue
		}
		fmt.Fprintf(out, "→ trusting the .%s root (macOS asks for your password)…\n", suffix)
		if err := trustRootHook(certPath); err != nil {
			return fmt.Errorf("trust the .%s CA: %w", suffix, err)
		}
	}
	return nil
}

// The per-OS trust hooks: untrustRootHook takes one swept certificate out of the user's
// and the admin's trust settings and off the login keychain (the argv layer is pinned by
// TestUntrustRootArgs); trustRootHook trusts a root the way trustCA has always trusted
// the global one; rootsTrusted answers whether a fingerprint already carries trust, so
// converged roots cost no dialog.
var (
	untrustRootHook = func(_ io.Writer, _, certPath, fingerprint string) error {
		return untrustRoot(certPath, fingerprint)
	}
	trustRootHook = func(certPath string) error {
		return trustCA(certPath)
	}
	rootsTrusted = func(fingerprint string) bool {
		return certs.TrustedByUser(fingerprint) || certs.TrustedByAdmin(fingerprint)
	}
)

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
