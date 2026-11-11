package cli

import (
	"fmt"
	"io"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
)

func newDomainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "domain",
		Aliases: []string{"domains"},
		Short:   "Manage a site's extra domains: subdomains (tenant1) and aliases (my-app-2.test)",
		Long: fmt.Sprintf(`Every site already serves its subdomains, up to %d levels deep (a.b.c.d.app.test).
Extra domains are for names Windows must find in the hosts file, which the hosts helper writes:
needed where wildcard DNS is overridden (company DNS policies, VPNs), and for aliases.

  flock domain add tenant1           tenant1.<site>.test
  flock domain add a.b               a.b.<site>.test
  flock domain add my-app-2.test     an alias, with its own subdomains
  flock domain add app-2 --suffix loc     an alias pinned to .loc (it follows nothing)
  flock domain add app-2 --suffix inherit an alias that rides the site's suffix
  flock domain remove tenant1
  flock domain list`, config.MaxDepth),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return listDomains(cmd, nil) },
	}
	cmd.AddCommand(
		newDomainAddCommand(),
		&cobra.Command{
			Use:     "remove <domain> [site]",
			Aliases: []string{"rm"},
			Short:   "Remove an extra domain from a site",
			Args:    cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := load()
				if err != nil {
					return err
				}
				site, err := siteArg(s, args[1:])
				if err != nil {
					return err
				}
				if !s.cfg.RemoveDomain(&site, args[0]) {
					return fmt.Errorf("%s is not an extra domain of %s", args[0], site.Name)
				}
				if err := s.cfg.Upsert(site); err != nil {
					return err
				}
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				cmd.Printf("removed %s from %s\n", args[0], site.Name)
				return nil
			},
		},
		&cobra.Command{
			Use:     "list [site]",
			Aliases: []string{"ls"},
			Short:   "List a site's extra domains (every site's, outside a site's directory)",
			Args:    cobra.MaximumNArgs(1),
			RunE:    func(cmd *cobra.Command, args []string) error { return listDomains(cmd, args) },
		},
	)
	return cmd
}

// newDomainAddCommand builds `flock domain add <domain> [site]`, with the --suffix flag
// that makes the entry an alias on a suffix of the user's choosing: a suffix pins it
// (Domain.TLD carries the suffix, and no global or site suffix change ever moves it — the
// Q2 rule), and the "inherit" keyword stores the inherited marker (an alias that follows
// the site's effective suffix). Without the flag, the full-host form decomposes as it
// always did (NormalizeDomain: the site's effective suffix makes an inherited alias, any
// other validated non-public suffix pins one, the rest is a subdomain label).
func newDomainAddCommand() *cobra.Command {
	var suffix string

	cmd := &cobra.Command{
		Use:   "add <domain> [site]",
		Short: "Add an extra domain to a site (the one in the current directory by default)",
		Long: `Without --suffix, <domain> is a subdomain label ("tenant1") or a full host: ending
in the site's effective suffix it becomes an alias that follows the site, any other
validated non-public suffix pins the alias to that suffix.

With --suffix, <domain> is a single label and the suffix says what the alias rides on:
--suffix loc pins it (it never moves with the site), --suffix inherit marks it inherited.

A suffix the config did not use before is new to Flock's provisioning: the add prints that
running "flock install" sets its DNS and HTTPS up.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteArg(s, args[1:])
			if err != nil {
				return err
			}

			withSuffix := cmd.Flags().Changed("suffix")
			oldActive := s.cfg.ActiveSuffixes() // the provisioning set before the entry lands
			var extra config.ExtraDomain
			if withSuffix {
				extra, err = addDomainWithSuffix(s.cfg, &site, args[0], suffix)
			} else {
				extra, err = s.cfg.AddDomain(&site, args[0])
			}
			if err != nil {
				return err
			}
			site.Parked = false // a parked site becomes linked so the domain can be stored
			if err := s.cfg.Upsert(site); err != nil {
				return err
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			scheme := "http"
			if site.Secured {
				scheme = "https"
			}
			cmd.Printf("%s → %s://%s\n", site.Name, scheme, extra.Host)

			// A pinned alias adds its suffix to the active set; the daemon created its root on
			// the reload commit ran, but the DNS and OS-trust provisioning is the CLI's
			// privileged job — say so once, the way the stale-suffix warnings do. The bare
			// full-host form (`flock domain add host.loc`) lands a new suffix too:
			// NormalizeDomain pins it into the stored entry, so the entry says what the add
			// introduced, and both forms print the same guidance line.
			newSuffix := suffixTLD(suffix)
			if !withSuffix {
				newSuffix = site.Domains[len(site.Domains)-1].TLD
			}
			suffixNewNote(cmd.OutOrStdout(), oldActive, newSuffix)
			return nil
		},
	}

	cmd.Flags().StringVar(&suffix, "suffix", "", "the alias's suffix: a validated suffix like loc (pinned) or inherit (follows the site); takes a single-label domain")

	return cmd
}

// suffixNewNote prints the provisioning guidance for a suffix the active set did not carry
// before the entry landed: its DNS and HTTPS setup is `flock install`'s job. The --suffix
// form and the bare full-host form (NormalizeDomain pins a new non-public suffix) share
// the line, and an inherited alias (no suffix of its own) prints nothing.
func suffixNewNote(out io.Writer, oldActive []string, suffix string) {
	if suffix != "" && !slices.Contains(oldActive, suffix) {
		fmt.Fprintf(out, "! .%s is new to Flock's setup: run `flock install` to provision its DNS and HTTPS\n", suffix)
	}
}

// suffixTLD is the suffix --suffix stores: "" for inherit (and for an empty value, which
// reads as inherit), the normalized suffix otherwise; validation is addDomainWithSuffix's.
func suffixTLD(raw string) string {
	if sfx := strings.ToLower(strings.TrimSpace(raw)); sfx != "" && sfx != "inherit" {
		return config.NormalizeTLD(sfx)
	}
	return ""
}

// domainLabelShape is one DNS label: with --suffix the domain is label-only (ruling R7 —
// the suffix parameter is only a suffix), the shape config's hostLabel checks for a label.
var domainLabelShape = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// addDomainWithSuffix stores an explicit-suffix alias for site: the "inherit" keyword (or
// an empty suffix) marks the alias inherited — Domain.TLD stays "" and the host rides the
// site's effective suffix at use time; any other suffix is normalized and validated
// (ValidateTLD refuses public and malformed ones) and pins the alias: the entry carries
// its own suffix and no global or site suffix change ever moves it (the Q2 rule). The
// resolved host goes through NormalizeDomain's checks — no site, proxy, or extra domain
// of the site may serve it — and the stored entry forces the marker the target string
// alone cannot state. Mirrors the API's addDomainSuffix (POST /v1/sites/{name}/domains).
func addDomainWithSuffix(cfg *config.Config, site *config.Site, domain, suffix string) (config.ExtraDomain, error) {
	label := strings.ToLower(strings.TrimSpace(domain))
	if !domainLabelShape.MatchString(label) {
		return config.ExtraDomain{}, fmt.Errorf("%q is not a domain label: with --suffix the domain is a single label of a-z, 0-9 and inner hyphens", domain)
	}
	tld := suffixTLD(suffix)
	if tld != "" {
		if _, err := config.ValidateTLD(tld); err != nil {
			return config.ExtraDomain{}, err
		}
	}
	target := label + "." + site.Suffix(cfg) // inherit: the site's effective suffix
	if tld != "" {
		target = label + "." + tld
	}
	entry, err := cfg.NormalizeDomain(*site, target)
	if err != nil {
		return config.ExtraDomain{}, err
	}
	if !entry.Alias { // can't happen: a validated single-label suffix always reads as an alias
		return config.ExtraDomain{}, fmt.Errorf("%s is not a suffix %s can use", suffix, site.Name)
	}
	entry.TLD = tld // force the marker: inherit → inherited, a suffix → pinned
	site.Domains = append(site.Domains, entry)
	extras := cfg.ExtraDomains(*site)
	return extras[len(extras)-1], nil
}

// listDomains prints the extra domains of the site named in args, the one in the working
// directory, or (outside any site) of every site.
func listDomains(cmd *cobra.Command, args []string) error {
	s, err := load()
	if err != nil {
		return err
	}
	only := ""
	if site, err := siteArg(s, args); err == nil {
		only = site.Name
	} else if len(args) > 0 {
		return err
	}

	out := cmd.OutOrStdout()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SITE\tDOMAIN\tSTATUS")

	st, err := s.client().Status(cmd.Context())
	if err == nil {
		for _, site := range st.Sites {
			if only != "" && site.Name != only {
				continue
			}
			for _, d := range site.Domains {
				state := d.URL
				if d.Problem != "" {
					state = "⚠ " + d.Problem
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", site.Name, d.Entry, state)
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
		wildcardNote(out, st.DNS)
		return nil
	}

	// Daemon down: show what the config says.
	for _, site := range s.cfg.AllSites() {
		if only != "" && site.Name != only {
			continue
		}
		for _, d := range s.cfg.ExtraDomains(site) {
			fmt.Fprintf(w, "%s\t%s\t%s\n", site.Name, d.Entry, d.Host)
		}
	}
	return w.Flush()
}

// wildcardNote explains which subdomains resolve on this machine.
func wildcardNote(out io.Writer, dns api.DNSStatus) {
	if dns.Wildcard {
		fmt.Fprintf(out, "\nEvery subdomain up to %d levels below a site (and its aliases) resolves, extra domain or not.\n", dns.MaxDepth)
		return
	}
	if runtime.GOOS == "windows" {
		fmt.Fprintf(out, "\nWildcard DNS is off: %s.\nOnly site domains and extra domains resolve (through the hosts file) — add each subdomain you use.\n", dns.WildcardProblem)
		return
	}
	// macOS and Linux have no hosts file fallback: every name under the TLD goes through the resolver setup.
	fmt.Fprintf(out, "\nDNS for .%s is not working: %s.\nNo .%s domain resolves to Flock until that is fixed.\n", dns.TLD, dns.WildcardProblem, dns.TLD)
}
