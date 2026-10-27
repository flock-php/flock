package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"flock/internal/config"
)

func newWorktreeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Git worktrees of your sites, each served at <branch>.<site>.test",
		Long: `Every git worktree of a linked or parked site is served at <branch>.<site>.test, with
the site's PHP, Node, HTTPS and document root. Worktrees made with git or any other tool
are picked up within a few seconds.`,
	}
	cmd.AddCommand(newWorktreeListCommand(), newWorktreeOpenCommand())
	return cmd
}

func newWorktreeListCommand() *cobra.Command {
	var siteName string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the worktrees of every site, or of one with --site",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if siteName != "" {
				if _, ok := s.cfg.FindSite(siteName); !ok {
					return fmt.Errorf("no site named %q", siteName)
				}
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SITE\tBRANCH\tURL\tPATH")
			// The daemon knows what is actually served; without it, show what the config says.
			urls := map[string]string{}
			if st, err := s.client().Status(cmd.Context()); err == nil {
				for _, site := range st.Sites {
					urls[site.Name] = site.URL
					if site.Problem != "" {
						urls[site.Name] = "⚠ " + site.Problem
					}
				}
			}
			for _, site := range s.cfg.AllSites() {
				if !site.IsWorktree() || (siteName != "" && site.Parent != siteName) {
					continue
				}
				url, ok := urls[site.Name]
				if !ok {
					url = worktreeURL(s, site)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", site.Parent, branchColumn(site), url, site.Path)
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&siteName, "site", "", "only this site's worktrees")
	return cmd
}

func newWorktreeOpenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "open [name]",
		Short: "Open a worktree in the browser (the one you're in, by default)",
		Long: `Opens a worktree in the browser. Name it by its branch, the name it is served under
(feature-auth) or its domain; without a name, the worktree you're in is opened.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := worktreeArg(s, args)
			if err != nil {
				return err
			}
			url := worktreeURL(s, site)
			cmd.Println(url)
			return openFile(url)
		},
	}
}

// worktreeArg resolves the worktree a command targets: the named one, or the one containing the
// working directory. A name may be the branch, the slug it is served under or its domain; when it
// fits worktrees of several sites, the site the working directory is in decides.
func worktreeArg(s *state, args []string) (config.Site, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return config.Site{}, err
	}
	here, inSite := s.cfg.SiteForDir(cwd)

	if len(args) == 0 {
		if inSite && here.IsWorktree() {
			return here, nil
		}
		return config.Site{}, fmt.Errorf("%s is not in a worktree of a Flock site — name one, see `flock worktree list`", cwd)
	}

	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(args[0]), "."))
	var matches []config.Site
	for _, site := range s.cfg.AllSites() {
		if !site.IsWorktree() {
			continue
		}
		slug, _, _ := strings.Cut(site.Name, ".")
		if name == site.Name || name == s.cfg.Domain(site) || name == slug || name == strings.ToLower(site.Branch) {
			matches = append(matches, site)
		}
	}
	if len(matches) > 1 && inSite {
		parent := here.Name
		if here.IsWorktree() {
			parent = here.Parent
		}
		var mine []config.Site
		for _, m := range matches {
			if m.Parent == parent {
				mine = append(mine, m)
			}
		}
		if len(mine) > 0 {
			matches = mine
		}
	}

	switch len(matches) {
	case 0:
		return config.Site{}, fmt.Errorf("no worktree named %q — see `flock worktree list`", args[0])
	case 1:
		return matches[0], nil
	}
	domains := make([]string, len(matches))
	for i, m := range matches {
		domains[i] = s.cfg.Domain(m)
	}
	return config.Site{}, fmt.Errorf("%q fits several worktrees, name one by its domain: %s", args[0], strings.Join(domains, ", "))
}

func worktreeURL(s *state, site config.Site) string {
	scheme := "http"
	if site.Secured {
		scheme = "https"
	}
	return scheme + "://" + s.cfg.Domain(site)
}

func branchColumn(site config.Site) string {
	if site.Branch == "" {
		return "(detached)"
	}
	return site.Branch
}
