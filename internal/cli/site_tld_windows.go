package cli

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS (Windows): both suffix-bound DNS pieces are replaced wholesale by the one
// elevated run — dns.SetNRPT deletes every Flock rule and re-adds one per suffix, and
// hosts.InstallHelper reconfigures the service for the new set — so the provisioning needs
// nothing per retiring suffix. The machine's certificate store is not: it carries one entry
// per root, and certutil -delstore by fingerprint needs the same administrator rights as
// -addstore. So the retired roots' fingerprints are captured here (their files go right
// after), and ride the elevated privileged-setup that provisions the new set.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA, retired []string) error {
	certsDir := filepath.Join(s.root, "certs")

	var untrust []string
	for _, suffix := range retired {
		dir := filepath.Join(certsDir, "roots", suffix)
		fingerprint, err := certs.FileFingerprint(filepath.Join(dir, "ca.crt"))
		if err == nil {
			untrust = append(untrust, fingerprint)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return provisionPrivileged(cmd, s, ca, untrust)
}
