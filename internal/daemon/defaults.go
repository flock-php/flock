package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/php"
	"flock/internal/project"
	"flock/internal/web"
)

// defaults installs the default PHP and Node.js on the first run: the newest stable PHP and the
// newest Node.js LTS line the channel offers, each made the default once it is installed. It only
// acts while a default is unset; after that, versions are the user's to install and switch.
// It runs at start, after every manifest fetch that changed something, and after every install.
type defaults struct {
	mu sync.Mutex // serialises ensure
	// node is the Node.js install it started (PHP's runs as a php.Manager job).
	nodeMu      sync.Mutex
	nodeVersion string
	nodeRunning bool
	nodeErr     string
}

// ensureDefaults sets or installs whichever default is missing. It doesn't block on downloads.
func (d *daemon) ensureDefaults() {
	if d.php == nil || d.binaries == nil {
		return
	}
	d.defaults.mu.Lock()
	defer d.defaults.mu.Unlock()

	cfg, err := config.Load(d.root)
	if err != nil {
		return
	}
	if cfg.DefaultPHP == "" {
		d.ensureDefaultPHP()
	}
	if cfg.DefaultNode == "" {
		d.ensureDefaultNode()
	}
}

// ensureDefaultPHP makes the newest installed PHP the default (one the first run installed, or
// one the user installed meanwhile), or else starts installing the newest stable one. A failed
// install isn't retried by itself: the PHP pane offers to try again.
func (d *daemon) ensureDefaultPHP() {
	if installed, _ := php.Installed(d.root); len(installed) > 0 {
		_ = d.Mutate(func(cfg *config.Config) error {
			if cfg.DefaultPHP == "" {
				cfg.DefaultPHP = installed[0]
			}
			return nil
		})
		return
	}
	if minor := d.php.LatestStable(); minor != "" && d.php.Job(minor) == nil {
		_ = d.php.Install(minor)
	}
}

// ensureDefaultNode is ensureDefaultPHP for Node.js, which installs in one call rather than as a job.
func (d *daemon) ensureDefaultNode() {
	if versions, _ := node.Installed(d.root); len(versions) > 0 {
		_ = d.Mutate(func(cfg *config.Config) error {
			if cfg.DefaultNode == "" {
				cfg.DefaultNode = node.MajorOf(versions[0])
			}
			return nil
		})
		return
	}
	major := node.LatestLTS(d.binaries.Manifest())

	d.defaults.nodeMu.Lock()
	defer d.defaults.nodeMu.Unlock()
	if major == "" || d.defaults.nodeRunning || (d.defaults.nodeErr != "" && d.defaults.nodeVersion == major) {
		return
	}
	d.defaults.nodeVersion, d.defaults.nodeRunning, d.defaults.nodeErr = major, true, ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		version, err := (&node.Installer{Root: d.root, Catalog: d.binaries}).Install(ctx, major)
		d.defaults.nodeMu.Lock()
		d.defaults.nodeRunning = false
		if err != nil {
			d.defaults.nodeErr = err.Error()
		}
		d.defaults.nodeMu.Unlock()
		if err == nil {
			node.RemoveOlder(d.root, version)
			d.ensureDefaults()
		}
	}()
}

// defaultsStatus is what the apps show while the first run installs the defaults: nil once both are set.
func (d *daemon) defaultsStatus(cfg *config.Config) *api.DefaultsStatus {
	if cfg == nil || d.php == nil || d.binaries == nil || (cfg.DefaultPHP != "" && cfg.DefaultNode != "") {
		return nil
	}
	st := &api.DefaultsStatus{PHP: api.DefaultInstall{State: api.DefaultDone}, Node: api.DefaultInstall{State: api.DefaultDone}}
	if cfg.DefaultPHP == "" {
		st.PHP = d.defaultPHPInstall()
	}
	if cfg.DefaultNode == "" {
		d.defaults.nodeMu.Lock()
		switch {
		case d.defaults.nodeRunning:
			st.Node = api.DefaultInstall{Version: d.defaults.nodeVersion, State: api.DefaultInstalling}
		case d.defaults.nodeErr != "":
			st.Node = api.DefaultInstall{Version: d.defaults.nodeVersion, State: api.DefaultFailed, Error: d.defaults.nodeErr}
		default:
			st.Node = api.DefaultInstall{Version: node.LatestLTS(d.binaries.Manifest()), State: api.DefaultWaiting}
		}
		d.defaults.nodeMu.Unlock()
	}
	return st
}

// defaultPHPInstall is the state of the first run's PHP install.
func (d *daemon) defaultPHPInstall() api.DefaultInstall {
	minor := d.php.LatestStable()
	if minor == "" {
		return api.DefaultInstall{State: api.DefaultWaiting}
	}
	switch j := d.php.Job(minor); {
	case j == nil:
		return api.DefaultInstall{Version: minor, State: api.DefaultWaiting}
	case j.State == "failed":
		return api.DefaultInstall{Version: minor, State: api.DefaultFailed, Error: j.Error}
	default:
		return api.DefaultInstall{Version: minor, State: api.DefaultInstalling}
	}
}

// waitingFor describes a site that can't be served because its PHP isn't installed, for the page
// its names get instead (web.Waiting).
func (d *daemon) waitingFor(site config.Site, v project.Version, domain string, aliases []string) web.Waiting {
	w := web.Waiting{Domain: domain, Aliases: aliases, Name: site.Name, PHP: v.Version, State: web.WaitingMissing}
	if v.Source == project.Project {
		w.File = filepath.Base(v.File)
	}
	if d.php == nil {
		return w
	}
	if w.PHP == "" {
		// No default yet: the first run is installing it, or waiting for the manifest to.
		in := d.defaultPHPInstall()
		w.PHP = in.Version
		if in.State != api.DefaultFailed || w.PHP == "" {
			w.State = web.WaitingInstalling
		}
		return w
	}
	switch j := d.php.Job(w.PHP); {
	case j != nil && j.State == "running":
		w.State = web.WaitingInstalling
	case !d.php.Offered(w.PHP):
		w.State = web.WaitingUnavailable
	}
	return w
}

// waitingProblem is what the apps say about a site waiting for its PHP.
func waitingProblem(w web.Waiting) string {
	pinned := ""
	if w.File != "" {
		pinned = fmt.Sprintf(" (pinned in %s)", w.File)
	}
	switch {
	case w.State == web.WaitingInstalling && w.PHP == "":
		return "PHP is being installed"
	case w.State == web.WaitingInstalling:
		return fmt.Sprintf("PHP %s is being installed", w.PHP)
	case w.State == web.WaitingUnavailable:
		return fmt.Sprintf("PHP %s%s is not available for this computer", w.PHP, pinned)
	case w.PHP == "":
		return "No PHP is installed"
	}
	return fmt.Sprintf("PHP %s%s is not installed", w.PHP, pinned)
}

// rootProblem is what the apps say about a site whose document root doesn't exist.
func rootProblem(r project.DocRoot) string {
	where := "set on this machine"
	if r.Source == project.Project {
		where = "set in " + filepath.Base(r.File)
	}
	return fmt.Sprintf("Document root %s doesn't exist (%s)", r.Rel, where)
}
