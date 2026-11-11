package cli

import (
	"github.com/spf13/cobra"

	"flock/internal/certs"
)

// syncSuffixesOS (Windows): the whole-set provisioning — provisionPrivileged sweeps the
// root stores outside the active set, carries their fingerprints to the elevated run
// (certutil -delstore by fingerprint needs the same administrator rights as -addstore),
// re-provisions the active set's roots, and deletes the swept stores only once the
// elevated untrust went through, so a declined prompt leaves the sweep retryable. Both
// suffix-bound DNS pieces are replaced wholesale by the same elevated run — dns.SetNRPT
// deletes every Flock rule and re-adds one per suffix, and hosts.InstallHelper
// reconfigures the service for the new set — so DNS, roots and trust all converge.
func syncSuffixesOS(s *state, cmd *cobra.Command, ca *certs.CA) error {
	return provisionPrivileged(cmd, s, ca)
}
