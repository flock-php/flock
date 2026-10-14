package cli

import (
	"fmt"
	"io"
	"runtime"
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
  flock domain remove tenant1
  flock domain list`, config.MaxDepth),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return listDomains(cmd, nil) },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "add <domain> [site]",
			Short: "Add an extra domain to a site (the one in the current directory by default)",
			Args:  cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := load()
				if err != nil {
					return err
				}
				site, err := siteArg(s, args[1:])
				if err != nil {
					return err
				}
				extra, err := s.cfg.AddDomain(&site, args[0])
				if err != nil {
					return err
				}
				site.Parked = false // a parked site becomes linked so the domain can be stored
				s.cfg.Upsert(site)
				if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
					return err
				}
				scheme := "http"
				if site.Secured {
					scheme = "https"
				}
				cmd.Printf("%s → %s://%s\n", site.Name, scheme, extra.Host)
				return nil
			},
		},
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
				s.cfg.Upsert(site)
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
	// macOS has no hosts file fallback: every name under the TLD goes through /etc/resolver.
	fmt.Fprintf(out, "\nDNS for .%s is not working: %s.\nNo .%s domain resolves to Flock until that is fixed.\n", dns.TLD, dns.WildcardProblem, dns.TLD)
}
