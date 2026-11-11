package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/certs"
	"flock/internal/config"
	"flock/internal/php"
	"flock/internal/worktree"
)

// newSiteCommand builds `flock site`, the per-site controls; so far it has one subcommand,
// tld. (The sites list itself is `flock sites`.)
func newSiteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Per-site settings: the domain suffix a site is served under",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newSiteTLDCommand())
	return cmd
}

// newSiteTLDCommand builds `flock site tld`: show the current site's effective domain and
// the raw override, switch a site to a suffix of its own, or hand it back to the global
// suffix with --inherit. Everything a suffix switch reaches — the site's and its worktrees'
// .env files, the daemon, and the OS integration for the suffixes the config now uses — is
// the same machinery `flock tld` runs for the global switch, set-diffed: switching one site
// retires nothing a second site still needs.
func newSiteTLDCommand() *cobra.Command {
	var inherit bool

	cmd := &cobra.Command{
		Use:   "tld [suffix] [site]",
		Short: "Show or set the suffix one site is served under (by default it inherits the global TLD)",
		Long: `The site is the one in the current directory, or the one named. With no suffix, show
what it is served under now. With a suffix, serve it as <name>.<suffix> and provision that
suffix (DNS and HTTPS, with your password); with --inherit the site follows the global TLD
again, the default. Aliases pinned to a suffix of their own (flock domain add --suffix) never
move with the site.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			suffix, siteName := "", ""
			switch {
			case inherit && len(args) > 1:
				return fmt.Errorf("usage: flock site tld --inherit [site]")
			case inherit && len(args) == 1:
				siteName = args[0]
			case !inherit && len(args) > 1:
				suffix, siteName = args[0], args[1]
			case !inherit && len(args) == 1:
				suffix = args[0]
			}

			s, err := load()
			if err != nil {
				return err
			}
			if !inherit && suffix == "" {
				site, err := siteArg(s, siteArgs(siteName))
				if err != nil {
					return err
				}
				if site.TLD == "" {
					cmd.Printf("%s (inheriting the global .%s)\n", s.cfg.Domain(site), s.cfg.TLD)
				} else {
					cmd.Printf("%s (site suffix .%s; the global is .%s)\n", s.cfg.Domain(site), site.TLD, s.cfg.TLD)
				}
				return nil
			}

			// The suffix set before the mutation: the sync set-diffs it against the set after.
			oldActive := s.cfg.ActiveSuffixes()

			oldSuffix, warning, err := s.changeSiteTLD(suffix, siteName, inherit, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if oldSuffix == "" {
				return nil // changeSiteTLD said "already": it touched nothing
			}

			if err := syncSuffixes(s, cmd, oldActive); err != nil {
				// The config carries the switch already, and a declined or failed elevation
				// leaves the suffixes' OS integration as it was: say what is finished and how
				// to finish the rest (`flock install` runs the same privileged steps).
				site, _ := siteArg(s, siteArgs(siteName))
				return fmt.Errorf("%s is served at %s already; run `flock install` to finish the switch: %w",
					site.Name, s.cfg.Domain(site), err)
			}

			site, _ := siteArg(s, siteArgs(siteName))
			cmd.Printf("✓ %s → %s from now on\n", site.Name, s.cfg.Domain(site))
			// The daemon relays the .local mDNS warning through its status warnings for the
			// global TLD only — a site's own .local is its own news, so it is heard from the
			// CLI whenever the daemon does not already speak for that suffix.
			if warning != "" && (s.cfg.TLD != "local" || !s.daemonRunning(cmd.Context())) {
				cmd.Println("warning: " + warning)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&inherit, "inherit", false, "serve the site under the global TLD again (the default)")

	return cmd
}

// siteArgs adapts a resolved site name to siteArg's argument slice: "" means the site in
// the working directory, exactly as an absent name does.
func siteArgs(name string) []string {
	if name == "" {
		return nil
	}
	return []string{name}
}

// changeSiteTLD is the unprivileged core of `flock site tld <suffix>|--inherit [site]`:
// resolve the site, normalize and validate the suffix, store the site's override ("" for
// inherit), retarget the site's own hosts — primary, inherited aliases, worktrees — and
// hand the config to the daemon. It returns the site's old effective suffix ("" when the
// site already sits where asked, the signal for RunE to skip provisioning) and ValidateTLD's
// warning to show with the success. Validation and the host-collision check run before
// anything is touched: a rejected suffix changes nothing. The privileged half
// (syncSuffixes) is RunE's, so this core is testable on a temp root with no daemon.
func (s *state) changeSiteTLD(raw, siteName string, inherit bool, out io.Writer) (oldSuffix string, warning string, err error) {
	site, err := siteArg(s, siteArgs(siteName))
	if err != nil {
		return "", "", err
	}
	if site.IsWorktree() {
		return "", "", fmt.Errorf("%s is a git worktree of %s: it is served under %s's domain — change that site",
			site.Name, site.Parent, site.Parent)
	}

	suffix := "" // "" is inherit: the site's TLD override clears and the global suffix rules
	if !inherit {
		suffix = config.NormalizeTLD(raw)
		warning, err = config.ValidateTLD(suffix)
		if err != nil {
			return "", "", err
		}
	}

	if site.TLD == suffix {
		fmt.Fprintf(out, "%s already served at %s\n", site.Name, s.cfg.Domain(site))
		return "", "", nil
	}

	oldSuffix = site.Suffix(s.cfg)
	newSuffix := suffix
	if newSuffix == "" {
		newSuffix = s.cfg.TLD
	}

	if newSuffix == oldSuffix {
		// Pinning the global suffix: no host moves and the active-suffix set is unchanged,
		// but the site stops following future global changes — a real, storable choice.
		site.TLD, site.Parked = suffix, false
		if err := s.cfg.Upsert(site); err != nil {
			return "", "", err
		}
		if err := s.commit(context.Background(), out); err != nil {
			return "", "", err
		}
		fmt.Fprintf(out, "%s now pins .%s (it stops following the global suffix)\n", site.Name, suffix)
		// RunE's success half is skipped for this branch (nothing to provision), so ValidateTLD's
		// warning is heard here — and only where the daemon does not already relay it.
		if warning != "" && (s.cfg.TLD != "local" || !s.daemonRunning(context.Background())) {
			fmt.Fprintln(out, "warning: "+warning)
		}
		return "", "", nil
	}

	// Before any mutation: the hosts the site moves onto must be free — no other site's
	// domain, no alias of any site, no proxy (the API's siteProxyClash, CLI-side).
	if err := s.siteHostsFree(site, newSuffix); err != nil {
		return "", "", err
	}

	site.TLD, site.Parked = suffix, false // a parked site becomes linked so the override can be stored
	if err := s.cfg.Upsert(site); err != nil {
		return "", "", err
	}

	// The .env files after the store, before the commit: SwapsForSite derives both sides
	// from its arguments, so the site's stored override cannot leak into the old side.
	for _, swap := range worktree.SwapsForSite(s.cfg, site.Name, oldSuffix, newSuffix) {
		changed, err := worktree.RetargetDotenv(swap.Path, swap.Old, swap.New)
		if err != nil {
			// A .env Flock may not rewrite (foreign ownership, permissions) is the user's to
			// fix by hand; the switch itself stands, so report the file and keep going.
			fmt.Fprintf(out, "! could not retarget %s: %v\n", swap.Path, err)
			continue
		}
		if len(changed) > 0 {
			fmt.Fprintf(out, "✓ %s: %s\n", swap.Path, strings.Join(changed, ", "))
		}
	}

	// commit saves the config and reloads a running daemon: roots for the new suffix, the
	// site's routing and the PHP/Node bundle come up under it (its warnings print through
	// commit).
	if err := s.commit(context.Background(), out); err != nil {
		return "", "", err
	}
	return oldSuffix, warning, nil
}

// siteHostsFree refuses a suffix that would move site onto a host Flock already serves
// under newSuffix: another site's domain, any site's alias (pinned hosts included), or a
// proxy's domain. The site's own inherited aliases move with it, onto the same suffix, and
// are checked against the other owners too.
func (s *state) siteHostsFree(site config.Site, newSuffix string) error {
	taken := map[string]string{}
	for _, o := range s.cfg.AllSites() {
		if o.Name == site.Name {
			continue
		}
		taken[s.cfg.Domain(o)] = "site " + o.Name
		for _, e := range s.cfg.ExtraDomains(o) {
			if e.Alias {
				taken[e.Host] = "site " + o.Name
			}
		}
	}
	for _, p := range s.cfg.Proxies {
		taken[s.cfg.ProxyDomain(p)] = "proxy " + p.Name
	}

	moving := []string{site.Name + "." + newSuffix}
	for _, d := range site.Domains {
		if d.Alias && d.TLD == "" {
			moving = append(moving, d.Label+"."+newSuffix)
		}
	}
	for _, host := range moving {
		if owner, ok := taken[host]; ok {
			return fmt.Errorf("%s is already served by %s: %s can't move to .%s", host, owner, site.Name, newSuffix)
		}
	}
	return nil
}

// syncSuffixes is the privileged half of a suffix change, shared by `flock tld` (the global
// switch) and `flock site tld` (a site's own): given the active-suffix set captured before
// the mutation, it set-diffs it against the config's new set — retires what left it (the
// per-OS DNS integration, the per-suffix root certificate and its OS trust), provisions the
// whole new set (roots for every suffix, the PHP/Node bundle carrying all of them, the OS
// DNS and trust steps through the install entry points) — and prints what the daemon sees
// once the machine is in sync. A declined elevation returns through errDeclined wrapped by
// the callers into "… run `flock install` to finish the switch" (Plan 1's decline
// semantics). Tests drive the cores behind it, never this.
func syncSuffixes(s *state, cmd *cobra.Command, oldActive []string) error {
	out := cmd.OutOrStdout()
	newActive := s.cfg.ActiveSuffixes()
	retired := retiredSuffixes(oldActive, newActive)

	ca, err := ensureCA(cmd, s)
	if err != nil {
		return err
	}

	// Every active suffix needs its name-constrained root before any of them can be trusted,
	// and the PHP/Node bundle has to carry all of them at once: the daemon created the set on
	// the core's reload when it runs, and this is the same Ensure for the daemon-less case.
	certsDir := filepath.Join(s.root, "certs")
	roots := certs.OpenRoots(certsDir, s.cfg.TLD)
	if err := roots.Ensure(newActive); err != nil {
		return err
	}
	if err := roots.Bundle(php.CABundle(s.root), certs.SystemRoots()); err != nil {
		return err
	}

	// DNS integration first: the per-OS entry point retires the resolver/NRPT piece of every
	// suffix that left the set (macOS removes the marker-owned /etc/resolver file; the Linux
	// drop-in and the Windows rules are rewritten wholesale by the provisioning below, so
	// their retireTLD says nothing), and then the install checks provision the new set.
	for _, suffix := range retired {
		if err := retireTLD(s, out, suffix); err != nil {
			return err
		}
	}

	if err := syncSuffixesOS(s, cmd, ca, retired); err != nil {
		return err
	}

	// The daemon reloaded on the core's commit before the machine caught up; its warnings
	// then ("stale .loc setup: run `flock install`") describe the pre-sync state — commit
	// printed them. Say what the daemon sees now: only what commit's set did not carry.
	if status, err := s.client().Status(cmd.Context()); err == nil {
		for _, w := range status.Warnings {
			if !slices.Contains(s.lastWarnings, w) {
				fmt.Fprintln(out, "warning: "+w)
			}
		}
	}
	return nil
}

// retiredSuffixes lists the suffixes oldActive has and newActive no longer does, in
// oldActive's order: the pure set-diff retirement runs on.
func retiredSuffixes(oldActive, newActive []string) []string {
	var retired []string
	for _, suffix := range oldActive {
		if !slices.Contains(newActive, suffix) {
			retired = append(retired, suffix)
		}
	}
	return retired
}

// deleteRootDirs removes the root stores of retired suffixes, the directories
// certsDir/roots/<suffix> the Roots store creates for non-global suffixes. The global root
// lives at certsDir itself and belongs to the store, never to one suffix, so this only ever
// reaches under roots/. A suffix is one validated DNS label (see ActiveSuffixes), so a name
// that cannot be a directory within roots/ — empty, a dot, a separator — removes nothing:
// hand-built lists cannot escape the store. A missing directory is a no-op.
func deleteRootDirs(certsDir string, suffixes []string) error {
	var errs []error
	for _, suffix := range suffixes {
		if suffix == "" || strings.ContainsAny(suffix, `./\`) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(certsDir, "roots", suffix)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
