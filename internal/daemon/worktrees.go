package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/mcpclients"
	"flock/internal/node"
	"flock/internal/worktree"
)

// markWorktreesSince records, the first time a daemon that sets up worktrees runs, that it does
// from now on: worktrees that existed before are served but left as they are.
func (d *daemon) markWorktreesSince() {
	cfg := d.config()
	if cfg == nil || !cfg.WorktreesSince.IsZero() {
		return
	}
	_ = d.Mutate(func(c *config.Config) error {
		if c.WorktreesSince.IsZero() {
			c.WorktreesSince = time.Now()
		}
		return nil
	})
}

// syncWorktrees starts the setup of worktrees that need one and forgets those that are gone,
// keeping the database of one that had its own. It runs after every reload and never waits: a
// setup saves config.json, which reloads.
func (d *daemon) syncWorktrees(cfg *config.Config) {
	if d.worktrees == nil {
		return
	}
	current := map[string]bool{}
	created := map[string]time.Time{} // when git created each worktree, by path
	scanned := map[string]bool{}      // parents whose worktrees are in created
	var back []string                 // worktrees whose folder is there again
	for _, site := range cfg.AllSites() {
		if !site.IsWorktree() {
			continue
		}
		current[filepath.Clean(site.Path)] = true
		if d.worktrees.Get(site.Path) != nil {
			continue // set up (or failed) since the daemon started, or running
		}
		if e, known := cfg.WorktreeEntry(site.Path); known {
			switch {
			case e.Gone:
				back = append(back, e.Path) // set up again, with the database it kept
			case !e.SetUp:
				d.startSetup(site, "") // a restart interrupted it
			}
			continue
		}
		if cfg.WorktreesSince.IsZero() {
			continue
		}
		if !scanned[site.Parent] {
			scanned[site.Parent] = true
			parent, _ := cfg.FindSite(site.Parent)
			for _, wt := range config.Worktrees(parent.Path) {
				created[filepath.Clean(wt.Path)] = wt.Created
			}
		}
		if created[filepath.Clean(site.Path)].Before(cfg.WorktreesSince) {
			continue
		}
		// A flock.yml that can't be read still starts it: the setup fails and says why.
		if parent, ok := cfg.FindSite(site.Parent); ok {
			if settings, err := worktree.Load(parent.Path, site.Path); err == nil && !settings.Auto {
				continue
			}
		}
		d.startSetup(site, "")
	}

	for _, p := range d.worktrees.Paths() {
		if !current[p] {
			go d.worktrees.Forget(p)
		}
	}
	var gone []string
	for _, e := range cfg.Worktrees {
		if !current[filepath.Clean(e.Path)] && !e.Gone {
			gone = append(gone, e.Path)
		}
	}
	if len(gone) > 0 || len(back) > 0 {
		go func() {
			_ = d.Mutate(func(c *config.Config) error {
				for _, p := range gone {
					if e, ok := c.WorktreeEntry(p); ok && keepsDatabase(e.Engine, e.Database) {
						e.Gone = true
						c.SetWorktreeEntry(e)
					} else {
						c.RemoveWorktreeEntry(p)
					}
				}
				for _, p := range back {
					if e, ok := c.WorktreeEntry(p); ok {
						e.Gone, e.SetUp = false, false
						c.SetWorktreeEntry(e)
					}
				}
				return nil
			})
		}()
	}
}

// worktreeJob describes the setup (or removal) of the worktree at path of the site parentName,
// served as site (zero for one whose folder is gone).
func (d *daemon) worktreeJob(cfg *config.Config, parentName, path string, site config.Site) (worktree.Job, error) {
	parent, ok := cfg.FindSite(parentName)
	if !ok {
		return worktree.Job{}, fmt.Errorf("no site named %s", parentName)
	}
	settings, err := worktree.Load(parent.Path, path)
	if err != nil {
		return worktree.Job{}, err
	}
	entry, _ := cfg.WorktreeEntry(path)
	mode := entry.DBMode
	if mode == "" {
		mode = settings.Database
	}
	domain := entry.Slug + "." + cfg.Domain(parent)
	if site.Name != "" {
		domain = cfg.Domain(site)
	}
	return worktree.Job{
		Site: parent.Path, Path: path, Parent: parent.Name, Branch: site.Branch,
		Domain: domain, ParentDomain: cfg.Domain(parent), Secured: parent.Secured, Settings: settings,
		Flock:    mcpclients.Executable(d.root),
		BinDirs:  []string{filepath.Join(d.root, "bin"), node.GlobalBin(node.CurrentGlobalDir(d.root))},
		AsRoot:   runningAsRoot(),
		DBMode:   mode,
		Database: worktree.Database{Engine: entry.Engine, Name: entry.Database},
		Servers:  d.dbServers(cfg),
		OnDatabase: func(db worktree.Database) {
			_ = d.Mutate(func(c *config.Config) error {
				if e, ok := c.WorktreeEntry(path); ok {
					e.Engine, e.Database = db.Engine, db.Name
					c.SetWorktreeEntry(e)
				}
				return nil
			})
		},
	}, nil
}

// startSetup sets the worktree up (with the database mode dbMode, when given). A mode asked for
// while it runs (see SetupWorktree) is switched to at its end.
func (d *daemon) startSetup(site config.Site, dbMode string) worktree.Status {
	return d.runJob(site, dbMode, func(ctx context.Context, log io.Writer, step func(string), job worktree.Job) error {
		if err := job.Run(ctx, log, step); err != nil {
			return err
		}
		mode := d.takePendingDB(site.Path)
		if mode == "" || mode == job.DBMode {
			return nil
		}
		err := d.Mutate(func(c *config.Config) error {
			if e, ok := c.WorktreeEntry(site.Path); ok {
				e.DBMode = mode
				c.SetWorktreeEntry(e)
			}
			return nil
		})
		if err != nil {
			return err
		}
		job.DBMode = mode
		return job.SwitchDatabase(ctx, log, step, false)
	}, func(e *config.WorktreeEntry) { e.SetUp = true })
}

// setPendingDB remembers a database mode asked for while the worktree's setup runs; that setup
// takes it (takePendingDB) when it's done.
func (d *daemon) setPendingDB(path, mode string) {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	if d.pendingDB == nil {
		d.pendingDB = map[string]string{}
	}
	d.pendingDB[filepath.Clean(path)] = mode
}

func (d *daemon) takePendingDB(path string) string {
	d.pendingMu.Lock()
	defer d.pendingMu.Unlock()
	mode := d.pendingDB[filepath.Clean(path)]
	delete(d.pendingDB, filepath.Clean(path))
	return mode
}

// runJob runs work as the worktree's job. It records the worktree first (with dbMode, when given):
// so its URL stays when the branch changes, and a setup a restart interrupts runs again. done
// updates the entry once work succeeded.
func (d *daemon) runJob(site config.Site, dbMode string, work func(context.Context, io.Writer, func(string), worktree.Job) error, done func(*config.WorktreeEntry)) worktree.Status {
	path := site.Path
	setup := func(ctx context.Context, log io.Writer, step func(string)) error {
		slug, _, _ := strings.Cut(site.Name, ".")
		var cfg *config.Config
		err := d.Mutate(func(c *config.Config) error {
			e, ok := c.WorktreeEntry(path)
			if !ok {
				e = config.WorktreeEntry{Path: path, Site: site.Parent, Slug: slug}
			}
			if dbMode != "" {
				e.DBMode = dbMode
			}
			c.SetWorktreeEntry(e)
			cfg = c
			return nil
		})
		if err != nil {
			return err
		}
		job, err := d.worktreeJob(cfg, site.Parent, path, site)
		if err != nil {
			return err
		}
		return work(ctx, log, step, job)
	}
	finished := func(err error) {
		d.takePendingDB(path) // asked for too late for this job: the asker switches (see SetupWorktree)
		if err != nil {
			return
		}
		_ = d.Mutate(func(c *config.Config) error {
			if e, ok := c.WorktreeEntry(path); ok {
				done(&e)
				c.SetWorktreeEntry(e)
			}
			return nil
		})
	}
	return d.worktrees.Start(path, site.Name, setup, finished)
}

func (d *daemon) config() *config.Config {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfg
}

// worktreeAt finds the worktree site whose folder is path.
func (d *daemon) worktreeAt(path string) (*config.Config, config.Site, error) {
	cfg := d.config()
	if cfg == nil {
		return nil, config.Site{}, errors.New("the daemon has no configuration yet")
	}
	site, ok := cfg.SiteForDir(path)
	if !ok || !site.IsWorktree() || !config.SamePath(site.Path, path) {
		return cfg, config.Site{}, fmt.Errorf("%w: %s", api.ErrNotWorktree, path)
	}
	return cfg, site, nil
}

// busy reports whether the worktree's setup is queued or running.
func (d *daemon) busy(path string) (worktree.Status, bool) {
	if st := d.worktrees.Get(path); st != nil && (st.State == worktree.Queued || st.State == worktree.Running) {
		return *st, true
	}
	return worktree.Status{}, false
}

func validDBMode(mode string) error {
	switch mode {
	case worktree.Shared, worktree.Copy, worktree.Fresh:
		return nil
	}
	return fmt.Errorf("%w: the database mode is shared, copy or fresh, not %q", api.ErrInvalidRequest, mode)
}

// SetupWorktree implements api.Backend: it starts the worktree's setup, or joins the one that
// runs. A worktree that is set up already is left alone unless again, or switched to dbMode
// when that is another mode. A setup that runs switches to dbMode when it's done.
func (d *daemon) SetupWorktree(path string, again bool, dbMode string) (worktree.Status, error) {
	if dbMode != "" {
		if err := validDBMode(dbMode); err != nil {
			return worktree.Status{}, err
		}
	}
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return worktree.Status{}, err
	}
	if st, ok := d.busy(site.Path); ok {
		if dbMode != "" {
			d.setPendingDB(site.Path, dbMode)
		}
		return st, nil
	}
	if !again {
		if st := d.worktreeStatus(cfg, site); st != nil && st.State == worktree.Ready {
			if job, err := d.worktreeJob(cfg, site.Parent, site.Path, site); err == nil && dbMode != "" && dbMode != job.DBMode {
				return d.SwitchWorktreeDatabase(path, dbMode, false)
			}
			return *st, nil
		}
	}
	return d.startSetup(site, dbMode), nil
}

// CreateWorktree implements api.Backend: it creates a worktree of the site on branch (see
// worktree.Create) and starts its setup.
func (d *daemon) CreateWorktree(ctx context.Context, req api.WorktreeCreate) (string, worktree.Status, error) {
	if req.DB != "" {
		if err := validDBMode(req.DB); err != nil {
			return "", worktree.Status{}, err
		}
	}
	cfg := d.config()
	if cfg == nil {
		return "", worktree.Status{}, errors.New("the daemon has no configuration yet")
	}
	site, ok := cfg.FindSite(req.Site)
	if !ok || site.IsWorktree() {
		return "", worktree.Status{}, fmt.Errorf("%w: no site named %s that worktrees can be made of", api.ErrInvalidRequest, req.Site)
	}
	if runningAsRoot() {
		return "", worktree.Status{}, fmt.Errorf("%w: Flock runs as root here, so it doesn't create worktrees: use `git worktree add`", api.ErrConflict)
	}
	path, err := worktree.Create(ctx, site.Path, site.Name, config.WorktreeSlug(req.Branch), req.Branch, req.From, req.Path)
	if errors.Is(err, worktree.ErrInvalid) {
		return "", worktree.Status{}, fmt.Errorf("%w: %v", api.ErrInvalidRequest, err)
	}
	if err != nil {
		return "", worktree.Status{}, err
	}
	if err := d.reload(); err != nil {
		return path, worktree.Status{}, err
	}
	st, err := d.SetupWorktree(path, false, req.DB)
	return path, st, err
}

// SwitchWorktreeDatabase implements api.Backend: as the worktree's job, it gives the worktree a
// database by mode, dropping the one Flock made for it unless keep.
func (d *daemon) SwitchWorktreeDatabase(path, mode string, keep bool) (worktree.Status, error) {
	if err := validDBMode(mode); err != nil {
		return worktree.Status{}, err
	}
	_, site, err := d.worktreeAt(path)
	if err != nil {
		return worktree.Status{}, err
	}
	if _, ok := d.busy(site.Path); ok {
		return worktree.Status{}, fmt.Errorf("%w: %s is being set up — wait for it with `flock worktree setup`", api.ErrConflict, site.Name)
	}
	return d.runJob(site, mode, func(ctx context.Context, log io.Writer, step func(string), job worktree.Job) error {
		return job.SwitchDatabase(ctx, log, step, keep)
	}, func(*config.WorktreeEntry) {}), nil
}

// WorktreeSetup implements api.Backend: the state of the worktree's setup, nil when Flock hasn't
// set it up.
func (d *daemon) WorktreeSetup(path string) (*worktree.Status, error) {
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return nil, err
	}
	return d.worktreeStatus(cfg, site), nil
}

func (d *daemon) worktreeStatus(cfg *config.Config, site config.Site) *worktree.Status {
	st := d.worktrees.Get(site.Path)
	e, known := cfg.WorktreeEntry(site.Path)
	if st == nil && known && e.SetUp {
		st = &worktree.Status{State: worktree.Ready, Log: worktree.LogName(site.Name)}
	}
	if st != nil && known {
		st.DBMode, st.Database = e.DBMode, e.Database
	}
	return st
}

// LeftWorktrees implements api.Backend: worktrees whose folder is gone, whose database Flock kept.
func (d *daemon) LeftWorktrees() []config.WorktreeEntry {
	out := []config.WorktreeEntry{}
	if cfg := d.config(); cfg != nil {
		for _, e := range cfg.Worktrees {
			if e.Gone {
				out = append(out, e)
			}
		}
	}
	return out
}

// RemoveWorktree implements api.Backend: it stops the worktree's setup, runs its before_delete
// hook, removes it with git and drops the database Flock made for it (unless keepDB, which keeps
// it listed). For a worktree whose folder is gone, only the database goes. It returns what the
// hook, git and the database tools printed.
func (d *daemon) RemoveWorktree(ctx context.Context, path string, force, keepDB bool) (string, error) {
	var log bytes.Buffer
	cfg, site, err := d.worktreeAt(path)
	if errors.Is(err, api.ErrNotWorktree) {
		e, ok := cfg.WorktreeEntry(path)
		if !ok || !e.Gone {
			return "", err
		}
		if keepDB {
			return "", nil
		}
		job, err := d.worktreeJob(cfg, e.Site, e.Path, config.Site{})
		if err != nil {
			return "", err
		}
		if err := job.DropDatabase(ctx, &log); err != nil {
			return log.String(), err
		}
		return log.String(), d.forgetWorktree(e.Path, false)
	}
	if err != nil {
		return "", err
	}

	job, err := d.worktreeJob(cfg, site.Parent, site.Path, site)
	if err != nil {
		return "", err
	}
	d.worktrees.Forget(site.Path)
	if err := job.Remove(ctx, &log, force); err != nil {
		return log.String(), err
	}
	if !keepDB {
		if err := job.DropDatabase(ctx, &log); err != nil {
			// Listed as left behind, for `flock worktree remove` to try again.
			_ = d.forgetWorktree(site.Path, true)
			return log.String(), fmt.Errorf("the worktree is removed, but its database isn't: %w", err)
		}
		job.Database = worktree.Database{}
	}
	return log.String(), d.forgetWorktree(site.Path, keepsDatabase(job.Database.Engine, job.Database.Name))
}

// keepsDatabase reports whether a worktree's database outlives its folder: a SQLite file Flock
// made is in the folder, and goes with it.
func keepsDatabase(engine, name string) bool {
	return name != "" && engine != "sqlite"
}

// forgetWorktree drops the worktree's entry, or with keep marks it gone (its database stays).
func (d *daemon) forgetWorktree(path string, keep bool) error {
	return d.Mutate(func(c *config.Config) error {
		if e, ok := c.WorktreeEntry(path); ok && keep {
			e.Gone = true
			c.SetWorktreeEntry(e)
		} else {
			c.RemoveWorktreeEntry(path)
		}
		return nil
	})
}

// withWorktrees adds the setup state to the worktrees among the statuses. The caller holds d.mu.
func (d *daemon) withWorktrees(sites []api.SiteStatus) []api.SiteStatus {
	if d.worktrees == nil || d.cfg == nil {
		return sites
	}
	for i, s := range sites {
		if s.Parent == "" {
			continue
		}
		sites[i].Worktree = d.worktreeStatus(d.cfg, config.Site{Name: s.Name, Path: s.Path})
	}
	return sites
}
