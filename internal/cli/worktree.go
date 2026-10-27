package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/worktree"
)

// exitNotWorktree is the exit code of `flock worktree setup` for a folder that isn't a worktree
// of a Flock site: setup scripts tell it from a setup that failed (1).
const exitNotWorktree = 3

// dbFlag is the help of --db.
const dbFlag = "the worktree's database: shared (the site's), copy (a copy of it) or fresh (empty, migrated); default: worktrees.database in flock.yml, else shared"

func newWorktreeAddCommand() *cobra.Command {
	var from, path, db string

	cmd := &cobra.Command{
		Use:   "add <branch>",
		Short: "Create a git worktree of the current site and set it up",
		Long: `Creates a worktree of the site you're in on <branch> (a new branch from --from, or the
branch if it exists), next to the site by default (worktrees.path in flock.yml), and waits
until Flock has set it up.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			site, ok := s.cfg.SiteForDir(cwd)
			if !ok {
				return fmt.Errorf("%s is not in a Flock site — run `flock link` first", cwd)
			}
			if site.IsWorktree() {
				site, _ = s.cfg.FindSite(site.Parent)
			}
			if path != "" {
				if path, err = filepath.Abs(path); err != nil {
					return err
				}
			}
			created, err := s.client().CreateWorktree(cmd.Context(), api.WorktreeCreate{Site: site.Name, Branch: args[0], From: from, Path: path, DB: db})
			if err != nil {
				return err
			}
			path = created.Path
			if err := setupWorktree(cmd, s, path, false, db); err != nil {
				return err
			}
			if s, err = load(); err == nil {
				if wt, ok := s.cfg.SiteForDir(path); ok && wt.IsWorktree() {
					cmd.Printf("ready: %s → %s\n", path, worktreeURL(s, wt))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "start a new branch from this commit or branch (default: HEAD)")
	cmd.Flags().StringVar(&path, "path", "", "where to create the worktree (default: worktrees.path in flock.yml, else ../<folder>-<branch>)")
	cmd.Flags().StringVar(&db, "db", "", dbFlag)
	return cmd
}

func newWorktreeSetupCommand() *cobra.Command {
	var again bool
	var db string

	cmd := &cobra.Command{
		Use:   "setup [path]",
		Short: "Set a worktree up, or wait for its setup (the one you're in, by default)",
		Long: `Sets up the worktree at path (or the one you're in), or waits for the setup Flock already
runs, showing what it does. A worktree that is set up already is left alone; --again sets
it up once more. For setup scripts: exits 0 when the worktree is ready, 1 when a setup
step failed, and 3 when the folder isn't a worktree of a Flock site.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			dir, err := dirArg(args)
			if err != nil {
				return err
			}
			site, ok := s.cfg.SiteForDir(dir)
			if !ok || !site.IsWorktree() {
				cmd.PrintErrf("Error: %s is not in a git worktree of a Flock site\n", dir)
				os.Exit(exitNotWorktree)
			}
			return setupWorktree(cmd, s, site.Path, again, db)
		},
	}
	cmd.Flags().BoolVar(&again, "again", false, "set it up again although it is set up already")
	cmd.Flags().StringVar(&db, "db", "", dbFlag)
	return cmd
}

func newWorktreeDBCommand() *cobra.Command {
	var keep bool

	cmd := &cobra.Command{
		Use:   "db <shared|copy|fresh> [name]",
		Short: "Switch a worktree's database (the one you're in, by default)",
		Long: `Gives a worktree another database: shared uses the site's, copy a copy of the site's,
fresh an empty one with the migrations run. The database Flock made for it before is
dropped, unless --keep-db.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			site, err := worktreeArg(s, args[1:])
			if err != nil {
				return err
			}
			if e, ok := s.cfg.WorktreeEntry(site.Path); ok && e.Database != "" && !keep && interactive() &&
				!confirm(fmt.Sprintf("Drop %s, the database Flock made for %s?", e.Database, site.Name), false) {
				return errors.New("nothing changed (--keep-db keeps the database)")
			}
			client := s.client()
			st, err := client.SwitchWorktreeDatabase(cmd.Context(), site.Path, args[0], keep)
			if err != nil {
				return err
			}
			return followSetup(cmd.Context(), cmd.OutOrStdout(), s, client, site.Path, st)
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-db", false, "keep the database Flock made for the worktree before")
	return cmd
}

func newWorktreeRemoveCommand() *cobra.Command {
	var force, keepDB bool

	cmd := &cobra.Command{
		Use:     "remove [name]",
		Aliases: []string{"rm"},
		Short:   "Run a worktree's before_delete hook, remove it with git and drop its own database",
		Long: `Runs the worktree's before_delete hook, removes it with git (the branch stays) and drops
the database Flock made for it, unless --keep-db. Named by its path, it also drops the
database of a worktree whose folder is gone (see flock worktree list).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			if len(args) > 0 {
				if e, ok := leftWorktree(s, args[0]); ok {
					out, err := s.client().RemoveWorktree(cmd.Context(), e.Path, force, keepDB)
					cmd.Print(out)
					if err == nil {
						cmd.Printf("forgot %s\n", e.Path)
					}
					return err
				}
			}
			site, err := worktreeArg(s, args)
			if err != nil {
				return err
			}
			// git can't remove the folder the shell is in on Windows, and the shell is left in a
			// folder that's gone elsewhere.
			if cwd, err := os.Getwd(); err == nil {
				if rel, err := filepath.Rel(site.Path, cwd); err == nil && !strings.HasPrefix(rel, "..") {
					return fmt.Errorf("you're in the worktree: run it from another folder, e.g. %s", filepath.Dir(site.Path))
				}
			}
			out, err := s.client().RemoveWorktree(cmd.Context(), site.Path, force, keepDB)
			if out != "" && err == nil {
				cmd.Print(out)
			}
			if err != nil {
				return err
			}
			cmd.Printf("removed %s (the branch %s stays)\n", site.Path, branchColumn(site))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove it even when it has changes or the before_delete hook fails")
	cmd.Flags().BoolVar(&keepDB, "keep-db", false, "keep the database Flock made for it")
	return cmd
}

// leftWorktree finds a worktree whose folder is gone but whose database Flock kept, by its path.
func leftWorktree(s *state, arg string) (config.WorktreeEntry, bool) {
	path, err := filepath.Abs(arg)
	if err != nil {
		return config.WorktreeEntry{}, false
	}
	for _, e := range s.cfg.Worktrees {
		if e.Gone && config.SamePath(e.Path, path) {
			return e, true
		}
	}
	return config.WorktreeEntry{}, false
}

// setupWorktree starts (or joins) the setup of the worktree at path and shows its log until it
// ends. The daemon may not have seen a worktree git just created: it is asked to look first.
func setupWorktree(cmd *cobra.Command, s *state, path string, again bool, db string) error {
	client := s.client()
	if _, err := client.Reload(cmd.Context()); err != nil {
		if errors.Is(err, api.ErrDaemonNotRunning) {
			return fmt.Errorf("the daemon isn't running, so %s isn't set up — start it with `flock restart`", path)
		}
		return err
	}
	st, err := client.SetupWorktree(cmd.Context(), path, again, db)
	if err != nil {
		return err
	}
	if st.State == worktree.Ready && !again {
		cmd.Printf("%s is set up already (--again sets it up once more)\n", path)
		return nil
	}
	if err := followSetup(cmd.Context(), cmd.OutOrStdout(), s, client, path, st); err != nil || db == "" {
		return err
	}
	// Asked for just as a setup the daemon started by itself ended: switched now.
	if st, err = client.WorktreeSetup(cmd.Context(), path); err != nil || st.DBMode == db {
		return err
	}
	if st, err = client.SetupWorktree(cmd.Context(), path, false, db); err != nil {
		return err
	}
	return followSetup(cmd.Context(), cmd.OutOrStdout(), s, client, path, st)
}

// followSetup prints the setup's log as it grows until the setup ends.
func followSetup(ctx context.Context, out io.Writer, s *state, client *api.Client, path string, st api.WorktreeStatus) error {
	logPath := filepath.Join(s.root, "log", st.Log+".log")
	var offset int64
	waiting := false
	for polls := 0; ; polls++ {
		// Every setup is queued for a moment; only a wait for others' says so.
		if st.State == worktree.Queued && !waiting && polls >= 3 {
			fmt.Fprintln(out, "waiting for other worktrees' setups to finish…")
			waiting = true
		}
		if st.State != worktree.Queued {
			offset = printFrom(out, logPath, offset)
		}
		switch st.State {
		case worktree.Ready:
			return nil
		case worktree.Failed:
			return fmt.Errorf("setting up %s failed: %s — the worktree is served anyway; see `flock logs %s`, fix it and run `flock worktree setup --again`", path, st.Error, st.Log)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		next, err := client.WorktreeSetup(ctx, path)
		if err != nil {
			return err
		}
		st = next
	}
}

// printFrom prints what was added to the file at path after offset and returns the new end; a
// file that got shorter started over.
func printFrom(out io.Writer, path string, offset int64) int64 {
	f, err := os.Open(path)
	if err != nil {
		return offset
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset
	}
	n, _ := io.Copy(out, f)
	return offset + n
}

func newWorktreeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Git worktrees of your sites, each served at <branch>.<site>.test",
		Long: `Every git worktree of a linked or parked site is served at <branch>.<site>.test, with
the site's PHP, Node, HTTPS and document root. Worktrees made with git or any other tool
are picked up within a few seconds and set up: Flock copies vendor/, node_modules/ and .env
in from the site, points .env at the worktree, runs the installs and the project's hooks
(see flock.yml).`,
	}
	cmd.AddCommand(newWorktreeAddCommand(), newWorktreeSetupCommand(), newWorktreeListCommand(), newWorktreeOpenCommand(),
		newWorktreeDBCommand(), newWorktreeRemoveCommand())
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
			fmt.Fprintln(w, "SITE\tBRANCH\tURL\tDATABASE\tPATH")
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
				db := "the site's"
				if e, ok := s.cfg.WorktreeEntry(site.Path); ok && e.Database != "" {
					db = e.Database
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", site.Parent, branchColumn(site), url, db, site.Path)
			}
			if err := w.Flush(); err != nil {
				return err
			}

			var left []config.WorktreeEntry
			for _, e := range s.cfg.Worktrees {
				if e.Gone && (siteName == "" || e.Site == siteName) {
					left = append(left, e)
				}
			}
			if len(left) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "\nDatabases of worktrees that are gone (drop one with `flock worktree remove <path>`):")
				w = tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SITE\tDATABASE\tWAS AT")
				for _, e := range left {
					fmt.Fprintf(w, "%s\t%s\t%s\n", e.Site, e.Database, e.Path)
				}
				return w.Flush()
			}
			return nil
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
