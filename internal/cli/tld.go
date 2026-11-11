package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/worktree"
)

// newTLDCommand shows and switches the local domain suffix Flock serves sites under. Changing it
// reaches everything that names a suffix: the saved config (site aliases), the sites' .env files,
// the daemon's DNS/CA/web setup and the OS integration that points the suffix at Flock.
func newTLDCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "tld [tld]",
		Short: "Show or switch the local domain Flock serves sites under (loc is the usual choice)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				cmd.Println("." + s.cfg.TLD)
				return nil
			}

			// The suffix set before the mutation: syncSuffixes set-diffs the config's new set
			// against it. The global switch retires the old TLD's integration only when no
			// site keeps it alive with an override of its own.
			oldActive := s.cfg.ActiveSuffixes()

			old, warning, err := s.changeTLD(args[0], cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if old == "" {
				return nil // changeTLD said "already serving .X": it touched nothing
			}

			// The privileged half, shared with `flock site tld` (site_tld.go): retire the
			// suffixes that left the set while their integration is the old one, then
			// provision the new set — ensureCA (certs.Ensure replaces the root CA on the TLD
			// mismatch) and ensurePrivileged re-running per-OS trust (macOS trustCA, Linux's
			// system anchors, Windows's certutil) with the config now on the new TLD.
			if err := syncSuffixes(s, cmd, oldActive); err != nil {
				// The config is on the new TLD already, and a declined or failed elevation leaves
				// the old suffix's integration in place: say what is finished and how to finish
				// the rest (`flock install` runs the same privileged steps).
				return fmt.Errorf("the config is on .%s already; run `flock install` to finish the switch: %w", s.cfg.TLD, err)
			}

			cmd.Printf("✓ serving .%s from now on\n", s.cfg.TLD)
			// The daemon's own .local mDNS warning reaches the user through state.commit's
			// status.Warnings; ValidateTLD's warning is printed only when no daemon answered to
			// relay it, so it is heard exactly once.
			if warning != "" && !s.daemonRunning(cmd.Context()) {
				cmd.Println("warning: " + warning)
			}
			return nil
		},
	}
}

// changeTLD is the unprivileged core of `flock tld <tld>`: normalize, validate, retarget the
// .env files, store the new TLD in the config and hand it to the daemon. It returns the TLD it
// left ("" when the input already is the current TLD, the signal for RunE to skip provisioning),
// and ValidateTLD's warning to show with the success. Validation runs before anything is
// touched: a rejected TLD changes nothing. The privileged half (syncSuffixes, shared with
// `flock site tld`) is RunE's, so this core is testable on a temp root with no daemon.
func (s *state) changeTLD(raw string, out io.Writer) (old string, warning string, err error) {
	tld := config.NormalizeTLD(raw)
	warning, err = config.ValidateTLD(tld)
	if err != nil {
		return "", "", err
	}
	if tld == s.cfg.TLD {
		fmt.Fprintf(out, "already serving .%s\n", tld)
		return "", "", nil
	}

	// The .env files come first: their app URLs, cookie domains and mail senders name hosts
	// under the old suffix, and TLDHostSwaps derives both sides from its arguments, so listing
	// the swaps while the old TLD is still the config's keeps the old side's suffix honest.
	swaps := worktree.TLDHostSwaps(s.cfg, s.cfg.TLD, tld)
	old = s.cfg.SetTLD(tld) // stores the suffix; inherited names follow it at use time
	for _, swap := range swaps {
		changed, err := worktree.RetargetDotenv(swap.Path, swap.Old, swap.New)
		if err != nil {
			// A .env Flock may not rewrite (foreign ownership, permissions) is the user's to fix
			// by hand; the switch itself stands, so report the file and keep going.
			fmt.Fprintf(out, "! could not retarget %s: %v\n", swap.Path, err)
			continue
		}
		if len(changed) > 0 {
			fmt.Fprintf(out, "✓ %s: %s\n", swap.Path, strings.Join(changed, ", "))
		}
	}

	// commit saves the config and reloads a running daemon: its DNS server, certificates and
	// web config come up under the new TLD (its .local warning, if any, prints through commit).
	if err := s.commit(context.Background(), out); err != nil {
		return "", "", err
	}
	return old, warning, nil
}

// daemonRunning reports whether the daemon answers on the API socket. `flock tld` prints
// ValidateTLD's warning itself only when there is no daemon to relay its equivalent (the
// warnings state.commit forwards), so the user hears it exactly once.
func (s *state) daemonRunning(ctx context.Context) bool {
	_, err := s.client().Status(ctx)
	return !errors.Is(err, api.ErrDaemonNotRunning)
}
