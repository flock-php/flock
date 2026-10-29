package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/project"
)

// The versions a site uses come, in this order, from (see package project):
//
//	isolate / unisolate   set for the site in Flock, on this machine only
//	pin / unpin           written into the project (.flockrc, .nvmrc), committed with it
//	                      and otherwise Flock's default
//
// pin and unpin also take the document root (see docroot.go).

func toolArg(tool string) error {
	if tool != project.PHP && tool != project.Node {
		return fmt.Errorf("unknown tool %q: use php or node", tool)
	}
	return nil
}

func siteFlag(cmd *cobra.Command, site *string) {
	cmd.Flags().StringVar(site, "site", "", "the site (default: the one in the current directory)")
}

func siteByFlag(s *state, name string) (config.Site, error) {
	if name != "" {
		return siteArg(s, []string{name})
	}
	return siteArg(s, nil)
}

func newIsolateCommand() *cobra.Command {
	var siteName string
	cmd := &cobra.Command{
		Use:   "isolate <php|node> <version>",
		Short: "Use a PHP or Node.js version for a site on this machine (e.g. `flock isolate php 8.4`, `flock isolate node 22`)",
		Long: "Sets the version for the site in Flock only, on this machine; it wins over the project's .flockrc / .nvmrc.\n" +
			"To set it for everyone working on the project, use `flock pin` instead.\n" +
			"`flock isolate 8.4` (without php) still isolates PHP.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool, version := "", ""
			switch {
			case len(args) == 2 && toolArg(args[0]) == nil:
				tool, version = args[0], args[1]
			case project.ValidVersion(project.PHP, args[0]) == nil:
				// The older form: `flock isolate 8.4 [name]`.
				tool, version = project.PHP, args[0]
				if len(args) == 2 {
					siteName = args[1]
				}
			default:
				return fmt.Errorf("usage: flock isolate <php|node> <version> (e.g. flock isolate php 8.4)")
			}
			if err := project.ValidVersion(tool, version); err != nil {
				return err
			}

			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteByFlag(s, siteName)
			if err != nil {
				return err
			}
			switch tool {
			case project.PHP:
				if installed, _ := php.Installed(s.root); !slices.Contains(installed, version) {
					return &phpMissingError{minor: version}
				}
				site.PHP = version
			case project.Node:
				if _, _, err := node.Resolve(s.root, version); err != nil {
					cmd.PrintErrf("note: Node.js %s isn't installed yet — `flock node install %s`, or run node there and Flock offers it\n", version, version)
				}
				site.Node = version
			}
			// A site from a project folder becomes linked so its settings can be stored.
			site.Parked = false
			s.cfg.Upsert(site)
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}

			cmd.Printf("%s now uses %s %s (isolated on this machine)\n", site.Name, toolName(tool), version)
			if v := versionFor(s.cfg, &site, tool); v.Pinned != "" && v.Pinned != version {
				cmd.Printf("note: the project pins %s %s in %s; the isolation wins until `flock unisolate %s`\n", toolName(tool), v.Pinned, filepath.Base(v.File), tool)
			}
			return nil
		},
	}
	siteFlag(cmd, &siteName)
	return cmd
}

func newUnisolateCommand() *cobra.Command {
	var siteName string
	cmd := &cobra.Command{
		Use:   "unisolate <php|node>",
		Short: "Let a site follow its project's pinned version (or the default) again",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool := args[0]
			if err := toolArg(tool); err != nil {
				return err
			}
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteByFlag(s, siteName)
			if err != nil {
				return err
			}
			if tool == project.PHP {
				site.PHP = ""
			} else {
				site.Node = ""
			}
			if !site.Parked {
				s.cfg.Upsert(site)
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			v := versionFor(s.cfg, &site, tool)
			cmd.Printf("%s now uses %s %s (%s)\n", site.Name, toolName(tool), v.Version, describeSource(v))
			return nil
		},
	}
	siteFlag(cmd, &siteName)
	return cmd
}

func newIsolatedCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "isolated",
		Short: "List every site's PHP and Node.js version, and where each comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "SITE\tPHP\tNODE")
			for _, site := range s.cfg.AllSites() {
				phpV, nodeV := project.PHPFor(s.cfg, &site, site.Path), project.NodeFor(s.cfg, &site, site.Path)
				fmt.Fprintf(w, "%s\t%s %s\t%s %s\n", site.Name, phpV.Version, describeSource(phpV), nodeV.Version, describeSource(nodeV))
			}
			return w.Flush()
		},
	}
}

func newPinCommand() *cobra.Command {
	var siteName string
	cmd := &cobra.Command{
		Use:   "pin <php|node|root> [version|folder]",
		Short: "Write a PHP or Node.js version, or the document root, into the project (.flockrc, .nvmrc), for everyone working on it",
		Long: "PHP goes into .flockrc; Node.js into the file the project already uses (.flockrc's node=,\n" +
			".node-version or .nvmrc), else a new .nvmrc. Node.js is pinned by major, so patch updates just work.\n" +
			"Without a version, the one the site uses now is pinned. A site's isolation for the tool is lifted.\n" +
			"`flock pin root <folder>` writes the folder the site is served from (relative to the project,\n" +
			"e.g. src/www) into .flockrc; the root set on this machine (`flock root`) is lifted.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool := args[0]
			if tool == project.Root {
				return pinRoot(cmd, siteName, args[1:])
			}
			if err := toolArg(tool); err != nil {
				return err
			}
			version := ""
			if len(args) == 2 {
				version = args[1]
			}

			s, err := load()
			if err != nil {
				return err
			}
			site, dir, err := projectTarget(s, siteName)
			if err != nil {
				return err
			}

			var file string
			if site != nil {
				var lifted bool
				if file, lifted, err = project.PinSite(s.cfg, site, tool, version); err != nil {
					return err
				}
				if lifted {
					s.cfg.Upsert(*site)
				}
			} else {
				if version == "" {
					version = versionFor(s.cfg, nil, tool).Version
					if tool == project.Node {
						version = node.MajorOf(version)
					}
				}
				if file, err = project.Pin(dir, tool, version); err != nil {
					return err
				}
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			cmd.Printf("pinned %s %s in %s (commit it to share it)\n", toolName(tool), pinnedValue(file, tool), file)
			return nil
		},
	}
	siteFlag(cmd, &siteName)
	return cmd
}

func newUnpinCommand() *cobra.Command {
	var siteName string
	cmd := &cobra.Command{
		Use:   "unpin <php|node|root>",
		Short: "Remove a PHP or Node.js version, or the document root, from the project's files (.flockrc, and for Node .nvmrc / .node-version)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool := args[0]
			if tool != project.Root {
				if err := toolArg(tool); err != nil {
					return err
				}
			}
			s, err := load()
			if err != nil {
				return err
			}
			_, dir, err := projectTarget(s, siteName)
			if err != nil {
				return err
			}
			changed, err := project.Unpin(dir, tool)
			if err != nil {
				return err
			}
			if len(changed) == 0 {
				if tool == project.Root {
					return fmt.Errorf("no document root is pinned in %s", dir)
				}
				return fmt.Errorf("no %s version is pinned in %s", toolName(tool), dir)
			}
			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			for _, file := range changed {
				if tool == project.Root {
					cmd.Printf("removed the document root from %s\n", file)
					continue
				}
				cmd.Printf("removed the %s version from %s\n", toolName(tool), file)
			}
			return nil
		},
	}
	siteFlag(cmd, &siteName)
	return cmd
}

// projectTarget is the project a pin applies to: the named site, the site in the current
// directory, or (outside any site) the current directory itself.
func projectTarget(s *state, siteName string) (*config.Site, string, error) {
	if siteName != "" {
		site, err := siteArg(s, []string{siteName})
		if err != nil {
			return nil, "", err
		}
		return &site, site.Path, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	if site, ok := s.cfg.SiteForDir(cwd); ok {
		return &site, site.Path, nil
	}
	return nil, cwd, nil
}

func versionFor(cfg *config.Config, site *config.Site, tool string) project.Version {
	dir, _ := os.Getwd()
	if site != nil {
		dir = site.Path
	}
	if tool == project.PHP {
		return project.PHPFor(cfg, site, dir)
	}
	return project.NodeFor(cfg, site, dir)
}

func toolName(tool string) string {
	if tool == project.Node {
		return "Node.js"
	}
	return "PHP"
}

// describeSource says where a version comes from: "default", ".flockrc",
// "isolated (overrides .nvmrc: 20)", ".flockrc (overrides .nvmrc: 20)".
func describeSource(v project.Version) string {
	switch v.Source {
	case project.Isolated:
		if v.Pinned != "" && v.Pinned != v.Version {
			return fmt.Sprintf("isolated (overrides %s: %s)", filepath.Base(v.File), v.Pinned)
		}
		return "isolated"
	case project.Project:
		if v.Shadowed != "" {
			return fmt.Sprintf("%s (overrides %s)", filepath.Base(v.File), v.Shadowed)
		}
		return filepath.Base(v.File)
	}
	return "default"
}

// pinnedValue reads back what a pin wrote, for the confirmation message.
func pinnedValue(file, tool string) string {
	switch tool {
	case project.PHP:
		return project.PHPFor(config.Default(), nil, filepath.Dir(file)).Pinned
	case project.Root:
		return project.RootFor(&config.Site{Path: filepath.Dir(file)}).Pinned
	}
	return project.NodeFor(config.Default(), nil, filepath.Dir(file)).Pinned
}
