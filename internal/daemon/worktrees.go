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
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
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

// syncWorktrees starts the setup of worktrees that need one (see needsSetup) and forgets those
// that are gone. It runs after every reload and never waits: a setup saves config.json, which
// reloads.
func (d *daemon) syncWorktrees(cfg *config.Config) {
	if d.worktrees == nil {
		return
	}
	current := map[string]bool{}
	created := map[string]time.Time{} // when git created each worktree, by path
	scanned := map[string]bool{}      // parents whose worktrees are in created
	for _, site := range cfg.AllSites() {
		if !site.IsWorktree() {
			continue
		}
		current[filepath.Clean(site.Path)] = true
		if d.worktrees.Get(site.Path) != nil {
			continue // set up (or failed) since the daemon started, or running
		}
		if e, known := cfg.WorktreeEntry(site.Path); known {
			if !e.SetUp {
				d.startSetup(cfg, site) // a restart interrupted it
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
		if settings, err := d.worktreeSettings(cfg, site); err == nil && !settings.Auto {
			continue
		}
		d.startSetup(cfg, site)
	}

	for _, p := range d.worktrees.Paths() {
		if !current[p] {
			go d.worktrees.Forget(p)
		}
	}
	var gone []string
	for _, e := range cfg.Worktrees {
		if !current[filepath.Clean(e.Path)] {
			gone = append(gone, e.Path)
		}
	}
	if len(gone) > 0 {
		go func() {
			_ = d.Mutate(func(c *config.Config) error {
				for _, p := range gone {
					c.RemoveWorktreeEntry(p)
				}
				return nil
			})
		}()
	}
}

func (d *daemon) worktreeSettings(cfg *config.Config, site config.Site) (worktree.Settings, error) {
	parent, ok := cfg.FindSite(site.Parent)
	if !ok {
		return worktree.Settings{}, fmt.Errorf("no site named %s", site.Parent)
	}
	return worktree.Load(parent.Path, site.Path)
}

// worktreeJob describes the setup (or removal) of the worktree site.
func (d *daemon) worktreeJob(cfg *config.Config, site config.Site) (worktree.Job, error) {
	parent, ok := cfg.FindSite(site.Parent)
	if !ok {
		return worktree.Job{}, fmt.Errorf("no site named %s", site.Parent)
	}
	settings, err := worktree.Load(parent.Path, site.Path)
	if err != nil {
		return worktree.Job{}, err
	}
	return worktree.Job{
		Site: parent.Path, Path: site.Path, Parent: parent.Name, Branch: site.Branch,
		Domain: cfg.Domain(site), ParentDomain: cfg.Domain(parent), Secured: site.Secured, Settings: settings,
		Flock:   mcpclients.Executable(d.root),
		BinDirs: []string{filepath.Join(d.root, "bin"), node.GlobalBin(node.CurrentGlobalDir(d.root))},
		AsRoot:  runningAsRoot(),
	}, nil
}

// startSetup sets the worktree up, recording it first: so its URL stays when the branch changes,
// and a setup a restart interrupts runs again.
func (d *daemon) startSetup(cfg *config.Config, site config.Site) worktree.Status {
	path := site.Path
	setup := func(ctx context.Context, log io.Writer, step func(string)) error {
		slug, _, _ := strings.Cut(site.Name, ".")
		err := d.Mutate(func(c *config.Config) error {
			if _, ok := c.WorktreeEntry(path); !ok {
				c.SetWorktreeEntry(config.WorktreeEntry{Path: path, Site: site.Parent, Slug: slug})
			}
			return nil
		})
		if err != nil {
			return err
		}
		job, err := d.worktreeJob(cfg, site)
		if err != nil {
			return err
		}
		return job.Run(ctx, log, step)
	}
	finished := func(err error) {
		if err != nil {
			return
		}
		_ = d.Mutate(func(c *config.Config) error {
			if e, ok := c.WorktreeEntry(path); ok {
				e.SetUp = true
				c.SetWorktreeEntry(e)
			}
			return nil
		})
	}
	return d.worktrees.Start(path, site.Name, setup, finished)
}

// worktreeAt finds the worktree site whose folder is path.
func (d *daemon) worktreeAt(path string) (*config.Config, config.Site, error) {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return nil, config.Site{}, errors.New("the daemon has no configuration yet")
	}
	site, ok := cfg.SiteForDir(path)
	if !ok || !site.IsWorktree() || !config.SamePath(site.Path, path) {
		return nil, config.Site{}, fmt.Errorf("%w: %s", api.ErrNotWorktree, path)
	}
	return cfg, site, nil
}

// SetupWorktree implements api.Backend: it starts the worktree's setup, or joins the one that
// runs. A worktree that is set up already is left alone unless again.
func (d *daemon) SetupWorktree(path string, again bool) (worktree.Status, error) {
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return worktree.Status{}, err
	}
	if st := d.worktrees.Get(site.Path); st != nil && (st.State == worktree.Queued || st.State == worktree.Running) {
		return *st, nil
	}
	if !again {
		if st := d.worktreeStatus(cfg, site); st != nil && st.State == worktree.Ready {
			return *st, nil
		}
	}
	return d.startSetup(cfg, site), nil
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
	if st := d.worktrees.Get(site.Path); st != nil {
		return st
	}
	if e, ok := cfg.WorktreeEntry(site.Path); ok && e.SetUp {
		return &worktree.Status{State: worktree.Ready, Log: worktree.LogName(site.Name)}
	}
	return nil
}

// RemoveWorktree implements api.Backend: it stops the worktree's setup, runs its before_delete
// hook and removes it with git. It returns what the hook and git printed.
func (d *daemon) RemoveWorktree(ctx context.Context, path string, force bool) (string, error) {
	cfg, site, err := d.worktreeAt(path)
	if err != nil {
		return "", err
	}
	job, err := d.worktreeJob(cfg, site)
	if err != nil {
		return "", err
	}
	d.worktrees.Forget(site.Path)
	var log bytes.Buffer
	if err := job.Remove(ctx, &log, force); err != nil {
		return log.String(), err
	}
	err = d.Mutate(func(c *config.Config) error {
		c.RemoveWorktreeEntry(site.Path)
		return nil
	})
	return log.String(), err
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
