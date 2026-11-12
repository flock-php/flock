package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/config"
	"flock/internal/worktree"
)

// newSiteHostCommand builds `flock site host`: show the current site's primary domain and
// the raw override, rename the domain name part of one site, or hand it back to the site
// name with --default. Only the name part moves — the suffix is `flock site tld`'s
// business. The move reaches everything that carries the site's name into a host: the
// site's own primary host, its subdomains and its worktrees' hosts — and their .env files,
// retargeted in the same run (the design's anti-goal: a renamed site whose .env still
// points at the dead host). Aliases never move: an alias is a name of its own. No
// provisioning and no elevation: the wildcard DNS already covers the new name and the leaf
// certificate re-issues on demand, so the run ends at the commit.
func newSiteHostCommand() *cobra.Command {
	var def bool

	cmd := &cobra.Command{
		Use:   "host [label] [site]",
		Short: "Show or rename the domain name part of one site (by default it is the site name)",
		Long: `The site is the one in the current directory, or the one named. With no label, show
the domain it serves at now and its override. With a label, serve it as <label>.<suffix> —
a single DNS label; the suffix stays where it is (flock site tld picks that). With
--default the site name rules again, the default. The site's subdomains and its worktrees'
hosts move with the name, and their .env files are retargeted from the old host to the new;
the site's aliases never move. The folder and the site name are untouched — this is the
domain part only.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			label, siteName := "", ""
			switch {
			case def && len(args) > 1:
				return fmt.Errorf("usage: flock site host --default [site]")
			case def && len(args) == 1:
				siteName = args[0]
			case !def && len(args) > 1:
				label, siteName = args[0], args[1]
			case !def && len(args) == 1:
				label = args[0]
			}

			s, err := load()
			if err != nil {
				return err
			}
			if !def && label == "" {
				site, err := siteArg(s, siteArgs(siteName))
				if err != nil {
					return err
				}
				if site.Host == "" {
					cmd.Printf("%s (the default: the site name)\n", s.cfg.Domain(site))
				} else {
					cmd.Printf("%s (site host %s; the default is %s)\n",
						s.cfg.Domain(site), site.Host, site.Name+"."+site.Suffix(s.cfg))
				}
				return nil
			}

			// No syncSuffixes and no elevation beside this core: a rename changes no
			// suffix — ActiveSuffixes reads the TLD fields only (config/domain_entry.go),
			// which this mutation never touches — so the provisioning set is invariant.
			_, newPrimary, changed, err := s.changeSiteHost(label, siteName, def, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if !changed {
				return nil // changeSiteHost said "already": it touched nothing
			}
			site, _ := siteArg(s, siteArgs(siteName))
			cmd.Printf("✓ %s → %s from now on\n", site.Name, newPrimary)
			return nil
		},
	}

	cmd.Flags().BoolVar(&def, "default", false, "serve the site under its site name again (the default)")

	return cmd
}

// changeSiteHost is the unprivileged core of `flock site host <label>|--default [site]`:
// resolve the site, normalize and validate the label (one DNS label; --default asks for the
// site name, stored canonically empty — sanitizeHost's form), probe the landing hosts,
// store the override, retarget the site's and its worktrees' .env files from the old host
// to the new, and hand the config to the daemon. Validation and the collision check run
// before anything is touched: a rejected label changes nothing. Both host sides of the
// .env move are HostRenameSwaps' arguments, so the site's stored override cannot leak into
// the old side (the listers run after the store, exactly like SwapsForSite's). It returns
// the site's primary host before and after, and whether anything changed at all (false on
// "already" — the signal for RunE to skip the success line).
func (s *state) changeSiteHost(raw, siteName string, def bool, out io.Writer) (oldPrimary, newPrimary string, changed bool, err error) {
	site, err := siteArg(s, siteArgs(siteName))
	if err != nil {
		return "", "", false, err
	}
	if site.IsWorktree() {
		return "", "", false, fmt.Errorf("%s is a git worktree of %s: it is served under %s's domain — change that site",
			site.Name, site.Parent, site.Parent)
	}

	host := "" // "" is the default: the site name (sanitizeHost's canonical form, stored empty)
	if !def {
		host = strings.ToLower(strings.TrimSpace(raw))
		if host == "" {
			return "", "", false, fmt.Errorf("usage: flock site host <label> [site], or flock site host --default [site]")
		}
		if !config.HostLabelOK(host) {
			return "", "", false, fmt.Errorf("%q is not a domain name: the name part of a site's host is a single DNS label — a-z, 0-9 and inner hyphens, up to 63 characters", raw)
		}
		if host == site.Name {
			host = "" // the site name is the default: store the default, not an override
		}
	}

	if site.Host == host {
		fmt.Fprintf(out, "%s already served at %s\n", site.Name, s.cfg.Domain(site))
		return "", "", false, nil
	}

	oldPrimary = s.cfg.Domain(site)
	oldHost := site.HostName()
	newHost := host
	if newHost == "" {
		newHost = site.Name // --default: the primary host rides the site name again
	}

	// Before any mutation: the host the site moves onto must be free — the same pair
	// siteHostsFree runs for a suffix switch (and the API's host patch runs too, so CLI and
	// API refuse exactly the same collisions).
	if err := s.siteHostRenameFree(site, newHost); err != nil {
		return "", "", false, err
	}

	site.Host, site.Parked = host, false // a parked site becomes linked so the override can be stored; host "" is the default
	if err := s.cfg.Upsert(site); err != nil {
		return "", "", false, err
	}

	// The .env files after the store, before the commit: both host sides come from the
	// arguments; the suffix HostRenameSwaps reads is the site's effective one, which a
	// rename never changes.
	for _, swap := range worktree.HostRenameSwaps(s.cfg, site.Name, oldHost, newHost) {
		rewritten, err := worktree.RetargetDotenv(swap.Path, swap.Old, swap.New)
		if err != nil {
			// A .env Flock may not rewrite (foreign ownership, permissions) is the user's to
			// fix by hand; the rename itself stands, so report the file and keep going.
			fmt.Fprintf(out, "! could not retarget %s: %v\n", swap.Path, err)
			continue
		}
		if len(rewritten) > 0 {
			fmt.Fprintf(out, "✓ %s: %s\n", swap.Path, strings.Join(rewritten, ", "))
		}
	}

	// commit saves the config and reloads a running daemon: the site's routing and the
	// PHP/Node bundle come up under the new name (its warnings print through commit).
	if err := s.commit(context.Background(), out); err != nil {
		return "", "", false, err
	}

	newPrimary = newHost + "." + site.Suffix(s.cfg)
	return oldPrimary, newPrimary, true, nil
}
