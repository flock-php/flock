package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/project"
)

// A site's document root comes, like its versions, from (see project.RootFor):
//
//	root / root --reset   set for the site in Flock, on this machine only
//	pin root / unpin root written into the project's .flockrc, committed with it
//	                      and otherwise detected: public/, www/, web/, webroot/, the project folder

func newRootCommand() *cobra.Command {
	var siteName string
	var reset bool
	cmd := &cobra.Command{
		Use:   "root [folder]",
		Short: "Show or set the folder a site is served from, on this machine (e.g. `flock root src/www`)",
		Long: "Flock serves a site from the first of public/, www/, web/ and webroot/ that has an index.php,\n" +
			"else from the project folder. `flock root <folder>` sets another folder for the site on this\n" +
			"machine only; it wins over the project's .flockrc. To set it for everyone working on the\n" +
			"project, use `flock pin root <folder>` instead.\n" +
			"The folder is relative to the project (an absolute path must lie in it); . is the project folder.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := siteByFlag(s, siteName)
			if err != nil {
				return err
			}

			switch {
			case reset && len(args) > 0:
				return fmt.Errorf("give a folder or --reset, not both")
			case reset:
				site.Root = ""
				if !site.Parked {
					s.cfg.Upsert(site)
				}
			case len(args) > 0:
				rel, err := project.RootFromPath(site.Path, args[0])
				if err != nil {
					return err
				}
				// A site from a project folder becomes linked so its settings can be stored.
				site.Root, site.Parked = rel, false
				s.cfg.Upsert(site)
			default:
				printRoot(cmd, site.Name, site.Path, project.RootFor(&site))
				return nil
			}

			if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
				return err
			}
			r := project.RootFor(&site)
			printRoot(cmd, site.Name, site.Path, r)
			if r.Source == project.Isolated && r.Pinned != "" && r.Pinned != r.Rel {
				cmd.Printf("note: the project sets root=%s in %s; this machine's root wins until `flock root --reset`\n", r.Pinned, filepath.Base(r.File))
			}
			return nil
		},
	}
	siteFlag(cmd, &siteName)
	cmd.Flags().BoolVar(&reset, "reset", false, "let the project's .flockrc or detection decide again")
	return cmd
}

// printRoot says where the site in path is served from or, when Flock can't serve it, why and
// which folders could be its root.
func printRoot(cmd *cobra.Command, name, path string, r project.DocRoot) {
	switch r.State {
	case project.RootMissing:
		cmd.Printf("%s isn't served: its document root %s (%s) doesn't exist, so Flock shows a page saying so\n", name, r.Rel, describeRoot(r))
	case project.RootNotFound:
		cmd.Printf("%s isn't served: no public/, www/, web/ or webroot/ has an index.php, nor the project folder, so Flock shows a page saying so\n", name)
	case project.RootEmpty:
		cmd.Printf("%s isn't served: %s (%s) has no index.php or index.html, so Flock shows a page saying so\n", name, rootLabel(r.Rel), describeRoot(r))
	default:
		cmd.Printf("%s is served from %s (%s)\n", name, rootLabel(r.Rel), describeRoot(r))
		return
	}
	for _, s := range project.SuggestRoots(path) {
		cmd.Printf("  found an index.php in %s: run `flock root %s` (this machine) or `flock pin root %s` (the project)\n", s, s, s)
	}
}

// rootLabel names a document root: "src/www", or "the project folder" for ".".
func rootLabel(rel string) string {
	if rel == "." {
		return "the project folder"
	}
	return rel
}

// describeRoot says where a document root comes from: "detected", ".flockrc", "set on this machine".
func describeRoot(r project.DocRoot) string {
	switch r.Source {
	case project.Isolated:
		return "set on this machine"
	case project.Project:
		return filepath.Base(r.File)
	}
	return "detected"
}

// rootColumn is a site's document root for `flock sites`: "www (detected)", "⚠ none found".
func rootColumn(r project.DocRoot) string {
	switch r.State {
	case project.RootMissing:
		return fmt.Sprintf("⚠ %s missing (%s)", r.Rel, describeRoot(r))
	case project.RootNotFound:
		return "⚠ none found"
	case project.RootEmpty:
		return fmt.Sprintf("%s (%s, no index)", strings.TrimPrefix(rootLabel(r.Rel), "the "), describeRoot(r))
	}
	return fmt.Sprintf("%s (%s)", strings.TrimPrefix(rootLabel(r.Rel), "the "), describeRoot(r))
}

// docRootOf reads a site's document root back from the daemon's status.
func docRootOf(st api.SiteStatus) project.DocRoot {
	return project.DocRoot{
		Dir: filepath.Join(st.Path, filepath.FromSlash(st.Root)), Rel: st.Root, Source: st.RootSource,
		Pinned: st.RootPinned, File: st.RootFile, State: st.RootState,
	}
}

// pinRoot is `flock pin root [folder]`: it writes the folder (by default the root the site uses
// now) into the project's .flockrc and lifts the root set for the site on this machine.
func pinRoot(cmd *cobra.Command, siteName string, args []string) error {
	s, err := load()
	if err != nil {
		return err
	}
	site, dir, err := projectTarget(s, siteName)
	if err != nil {
		return err
	}
	rel := ""
	if len(args) > 0 {
		if rel, err = project.RootFromPath(dir, args[0]); err != nil {
			return err
		}
	}

	var file string
	if site != nil {
		var lifted bool
		if file, lifted, err = project.PinSiteRoot(site, rel); err != nil {
			return err
		}
		if lifted {
			s.cfg.Upsert(*site)
		}
	} else {
		if rel == "" {
			rel = project.DetectRoot(dir)
		}
		if file, err = project.PinRoot(dir, rel); err != nil {
			return err
		}
	}
	if err := s.commit(cmd.Context(), cmd.ErrOrStderr()); err != nil {
		return err
	}
	cmd.Printf("pinned the document root %s in %s (commit it to share it)\n", rootLabel(pinnedValue(file, project.Root)), file)
	return nil
}
