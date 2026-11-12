package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/config"
)

// newSiteTitleCommand builds `flock site title`: show what the current site is shown as
// now, set its display name, or hand it back to the site name with --default. The title is
// display text only — the app list and the status screens show it — so nothing that is
// served moves: no host, no certificate, no .env, no provisioning. The words are joined
// and sanitized by config.SanitizeTitle, the same rule Load applies to a hand-edited
// config.json (control characters dropped, one line, capped): the stored value can never
// be worse than what the sanitizer accepts.
func newSiteTitleCommand() *cobra.Command {
	var def bool

	cmd := &cobra.Command{
		Use:   "title [words…] [site]",
		Short: "Show or set the display name of one site (by default it is the site name)",
		Long: `The site is the one in the current directory, or the one named: the last argument
names a site only when it is one, from the second argument on. With no words, show what
the app lists the site as now. With words, show the site under them — joined with single
spaces, one line, at most 80 characters; with --default the site name shows again, the
default. This changes display only: domains, certificates and .env files stay exactly
where they are.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			words, siteName := "", ""
			switch {
			case def && len(args) > 1:
				return fmt.Errorf("usage: flock site title --default [site]")
			case def && len(args) == 1:
				siteName = args[0]
			case !def && len(args) == 0:
				site, err := siteArg(s, nil)
				if err != nil {
					return err
				}
				if site.Title == "" {
					cmd.Printf("%s (shown as its site name)\n", site.Name)
				} else {
					cmd.Printf("%s (site title; the site name is %s)\n", site.Title, site.Name)
				}
				return nil
			case !def:
				words, siteName = titleWordsAndSite(s.cfg, args)
			}

			title, changed, err := s.changeSiteTitle(words, siteName, def, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if !changed {
				return nil // changeSiteTitle said "already": it touched nothing
			}
			site, _ := siteArg(s, siteArgs(siteName))
			if title == "" {
				cmd.Printf("✓ %s shown as its site name again\n", site.Name)
			} else {
				cmd.Printf("✓ %s shown as %q from now on\n", site.Name, title)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&def, "default", false, "show the site under its site name again (the default)")

	return cmd
}

// titleWordsAndSite splits a `flock site title` argument list into its display words and
// the site they name: the last argument is the site only from the second argument on and
// only when it names one — a single word is always the title ("flock site title Liga"
// retitles the current site, it never targets the site liga).
func titleWordsAndSite(cfg *config.Config, args []string) (words, siteName string) {
	if len(args) >= 2 {
		if _, ok := cfg.FindSite(args[len(args)-1]); ok {
			return strings.Join(args[:len(args)-1], " "), args[len(args)-1]
		}
	}
	return strings.Join(args, " "), ""
}

// changeSiteTitle is the core of `flock site title <words>|--default [site]`: sanitize the
// display name, store it ("" — what --default passes — for the site-name default) and
// commit. No landing probe and no retarget loop belong here: a title is display-only, it
// names no host and collides with nothing. A parked site becomes linked so the title can
// be stored, exactly as for a TLD or host override. It returns the stored title and
// whether anything changed (false on "already").
func (s *state) changeSiteTitle(raw, siteName string, def bool, out io.Writer) (title string, changed bool, err error) {
	site, err := siteArg(s, siteArgs(siteName))
	if err != nil {
		return "", false, err
	}
	if site.IsWorktree() {
		return "", false, fmt.Errorf("%s is a git worktree of %s: it is shown under %s's name — change that site",
			site.Name, site.Parent, site.Parent)
	}

	newTitle := ""
	if !def {
		// "words joined with single spaces": every whitespace run in the input (the argv
		// words, or spaces and tabs inside one quoted argument) folds to a single space
		// first; SanitizeTitle then applies the stored-text rules (control runes dropped,
		// trimmed, capped) exactly as Load does for a hand-edited config.json.
		newTitle = config.SanitizeTitle(strings.Join(strings.Fields(raw), " "))
	}
	if site.Title == newTitle {
		if newTitle == "" {
			fmt.Fprintf(out, "%s is already shown as its site name\n", site.Name)
		} else {
			fmt.Fprintf(out, "%s is already shown as %q\n", site.Name, newTitle)
		}
		return "", false, nil
	}

	site.Title, site.Parked = newTitle, false // a parked site becomes linked so the title can be stored
	if err := s.cfg.Upsert(site); err != nil {
		return "", false, err
	}
	if err := s.commit(context.Background(), out); err != nil {
		return "", false, err
	}
	return newTitle, true, nil
}
