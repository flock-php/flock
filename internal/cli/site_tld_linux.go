package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS (Linux): converge the whole set through the install checks —
// ensurePrivileged now runs the roots pass (provisionRootsOS: sweep every store outside
// the set out of the browsers, ensure the active roots and their browser trust) and the
// privileged provisioning carries the intersection, so the system side — the resolved
// drop-in's Domains= line and the trust store's anchor copies — is rewritten wholesale for
// the active set. The system side is one file per user's whole suffix set: the
// provisioning retires the routing and the anchors of suffixes that left it with the same
// wholesale write, so there is nothing per retiring suffix to remove by hand.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA) error {
	return ensurePrivileged(cmd, s, "", ca)
}

// provisionRootsOS is the whole-set roots pass `flock install` (install_linux.go's
// ensurePrivileged) and the provisioning sync share (F2): create-if-missing for every
// active root, the sweep retiring every store outside the set (the browsers' NSS entries
// go first — certutil, no password — and the store directories with them), and the
// browser trust ensured for every root of the set. The hooks are package-level so tests
// drive the wiring without touching the real databases.
func provisionRootsOS(s *state, out io.Writer) error {
	certsDir := filepath.Join(s.root, "certs")
	active := s.cfg.ActiveSuffixes()
	if err := certs.OpenRoots(certsDir, s.cfg.TLD).Ensure(active); err != nil {
		return err
	}

	if _, err := sweepStaleRoots(out, certsDir, active, sweepOpts{untrust: untrustRootHook, remove: true}); err != nil {
		return err
	}

	// The browsers' own databases are the user's: no password. The Firefox enterprise
	// policy keeps carrying the global root (one file per user in the certs package's
	// layout); extra roots reach Firefox through its database, the ones it has.
	for _, suffix := range active {
		if suffix == s.cfg.TLD {
			continue // the global root: ensurePrivileged's anchors and policy cover it
		}
		certPath := filepath.Join(certsDir, "roots", suffix, "ca.crt")
		if !fileExists(certPath) || rootsTrusted(certPath) {
			continue
		}
		if err := trustRootHook(certPath); err != nil {
			fmt.Fprintf(out, "! Firefox and Chromium browsers don't trust the .%s root: %v\n", suffix, err)
		}
	}
	return nil
}

// The per-OS trust hooks. A store whose browser entry certutil cannot remove (a database
// that never had it) is reported and retired all the same — the report-and-continue
// semantics the per-suffix sync always had.
var (
	untrustRootHook = func(out io.Writer, suffix, certPath, _ string) error {
		if err := certs.UntrustInBrowsers(certPath); err != nil {
			fmt.Fprintf(out, "! could not remove the .%s root from your browsers: %v\n", suffix, err)
		}
		return nil
	}
	trustRootHook = func(certPath string) error { return certs.TrustInBrowsers(certPath) }
	rootsTrusted  = certs.TrustedInBrowsers
)
