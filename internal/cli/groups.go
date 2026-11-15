package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"flock/internal/config"
)

func newGroupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "group",
		Aliases: []string{"groups"},
		Short:   "Organise sites into groups",
		Long: fmt.Sprintf(`A site can be in one group, to keep a long list of sites in order. A group exists
while a site is in it, so there is nothing to create or delete. Names are at most %d
characters; "work" joins an existing "Work". Groups only organise the lists of sites: nothing is
served differently.

  flock group set Clients            put the site in this directory in Clients
  flock group set Clients shop       put shop in Clients
  flock group clear shop             take shop out of its group
  flock group list                   every group with its sites
  flock group list Clients           one group's sites`, config.MaxGroupLength),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return listGroups(cmd, nil) },
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "set <group> [site]",
			Short: "Put a site in a group (the one in the current directory by default)",
			Args:  cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return assignGroup(cmd, args[0], args[1:])
			},
		},
		&cobra.Command{
			Use:   "clear [site]",
			Short: "Take a site out of its group",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return assignGroup(cmd, "", args)
			},
		},
		&cobra.Command{
			Use:     "list [group]",
			Aliases: []string{"ls"},
			Short:   "List the groups and their sites",
			Args:    cobra.MaximumNArgs(1),
			RunE:    func(cmd *cobra.Command, args []string) error { return listGroups(cmd, args) },
		},
	)
	return cmd
}

// assignGroup puts the site named in args (or the one in the working directory) in group, or out
// of its group when group is empty.
func assignGroup(cmd *cobra.Command, group string, args []string) error {
	s, err := load()
	if err != nil {
		return err
	}
	site, err := siteArg(s, args)
	if err != nil {
		return err
	}
	if err := s.cfg.SetGroup(&site, group); err != nil {
		return err
	}
	site.Parked = false // a parked site becomes linked so the group can be stored
	if err := s.cfg.Upsert(site); err != nil {
		return err
	}
	if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
		return err
	}
	if site.Group == "" {
		cmd.Printf("%s is in no group\n", site.Name)
	} else {
		cmd.Printf("%s is in %s\n", site.Name, site.Group)
	}
	return nil
}

// listGroups prints the groups with their sites, or the sites of the group named in args.
func listGroups(cmd *cobra.Command, args []string) error {
	s, err := load()
	if err != nil {
		return err
	}
	if len(args) > 0 {
		group, err := s.cfg.NormalizeGroup(args[0])
		if err != nil {
			return err
		}
		if len(s.cfg.SitesInGroup(group)) == 0 {
			return fmt.Errorf("no site is in the group %q", args[0])
		}
		writeGroups(cmd.OutOrStdout(), s.cfg, group)
		return nil
	}
	if len(s.cfg.Groups()) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no groups yet — `flock group set <group> [site]` puts a site in one")
		return nil
	}
	writeGroups(cmd.OutOrStdout(), s.cfg, "")
	return nil
}

// writeGroups prints the sites by group: every group, then the sites in none, or just only (a
// group name that exists) when it is set.
func writeGroups(out io.Writer, cfg *config.Config, only string) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "GROUP\tSITE\tURL")
	groups := cfg.Groups()
	if only != "" {
		groups = []string{only}
	}
	for _, g := range groups {
		for _, site := range cfg.SitesInGroup(g) {
			fmt.Fprintf(w, "%s\t%s\t%s\n", g, site.Name, siteURL(cfg, site))
		}
	}
	if only == "" {
		for _, site := range cfg.SitesInGroup("") {
			fmt.Fprintf(w, "-\t%s\t%s\n", site.Name, siteURL(cfg, site))
		}
	}
	w.Flush()
}

// siteURL is where a site is served, from the config alone.
func siteURL(cfg *config.Config, site config.Site) string {
	scheme := "http"
	if site.Secured {
		scheme = "https"
	}
	return scheme + "://" + cfg.Domain(site)
}
