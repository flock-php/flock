package daemon

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/envutil"
	"flock/internal/node"
	"flock/internal/supervisor"
)

// stopCommandTimeout is how long a proxy's stop command may take.
const stopCommandTimeout = time.Minute

// proxyRun is a started proxy's command. A foreground one is a supervised process (see
// proxySpec); a background one's command runs once, and running is set until it exits.
type proxyRun struct {
	proxy    config.Proxy // as it was started: its stop command is the one that stops it
	running  bool
	stopping bool
	problem  string             // why the background command failed
	cancel   context.CancelFunc // ends a background command still starting
	done     chan struct{}      // closed once the background command ended
}

// StartProxy implements api.Backend: it runs the proxy's command, and keeps a foreground one
// running until StopProxy.
func (d *daemon) StartProxy(name string) error {
	p, err := d.commandProxy(name)
	if err != nil {
		return err
	}
	if runningAsRoot() {
		return fmt.Errorf("Flock runs as root here, so it doesn't run %s's command — run Flock as your own user", name)
	}

	d.mu.Lock()
	if run, ok := d.proxyRuns[name]; ok && (run.running || run.stopping) {
		d.mu.Unlock()
		if run.stopping {
			return fmt.Errorf("%s is stopping — start it once it stopped", name)
		}
		return nil
	}
	defer d.probe.forget(p.Upstream)
	run := &proxyRun{proxy: p, running: p.StopCommand != ""}
	ctx, cancel := context.WithCancel(context.Background())
	if run.running {
		run.cancel, run.done = cancel, make(chan struct{})
	}
	if d.proxyRuns == nil {
		d.proxyRuns = map[string]*proxyRun{}
	}
	d.proxyRuns[name] = run
	d.mu.Unlock()

	if p.StopCommand == "" {
		cancel()
		return d.reload()
	}
	go func() {
		defer close(run.done)
		defer cancel()
		err := d.runProxyCommand(ctx, p, p.Command)
		d.mu.Lock()
		run.running = false
		if err != nil && ctx.Err() == nil {
			run.problem = err.Error()
		}
		d.mu.Unlock()
	}()
	return nil
}

// StopProxy implements api.Backend: it stops a foreground command, or runs the stop command of
// a background one. A background proxy stays started when its stop command fails, so it can be
// stopped again.
func (d *daemon) StopProxy(name string) error {
	p, err := d.commandProxy(name)
	if err != nil {
		return err
	}
	d.mu.Lock()
	run := d.proxyRuns[name]
	if run != nil {
		if run.stopping {
			d.mu.Unlock()
			return fmt.Errorf("%s is stopping already", name)
		}
		run.stopping = true
		p = run.proxy
	}
	d.mu.Unlock()
	defer d.probe.forget(p.Upstream)

	// Under reloadMu, so a reload doesn't start the foreground command again as it stops.
	d.reloadMu.Lock()
	if p.StopCommand == "" {
		d.forgetProxyRun(name, run)
	}
	d.procs.Stop(config.ProxyLog(name))
	d.reloadMu.Unlock()
	if p.StopCommand == "" {
		return nil
	}

	err = d.stopInBackground(run, p)
	if err == nil {
		d.forgetProxyRun(name, run)
		return nil
	}
	if run != nil {
		d.mu.Lock()
		run.stopping, run.problem = false, err.Error()
		d.mu.Unlock()
	}
	return err
}

// forgetProxyRun drops run, unless the proxy was started again since.
func (d *daemon) forgetProxyRun(name string, run *proxyRun) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.proxyRuns[name] == run {
		delete(d.proxyRuns, name)
	}
}

// stopInBackground runs p's stop command, once run's command, if still starting, was ended: else
// the server could start after it stopped.
func (d *daemon) stopInBackground(run *proxyRun, p config.Proxy) error {
	if run != nil && run.done != nil {
		run.cancel()
		<-run.done
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopCommandTimeout)
	defer cancel()
	return d.runProxyCommand(ctx, p, p.StopCommand)
}

// stopProxyRuns runs the stop commands of runs, the background proxies that were removed, lost
// their command, or are started as the daemon shuts down.
func (d *daemon) stopProxyRuns(runs []*proxyRun) {
	var wg sync.WaitGroup
	for _, run := range runs {
		wg.Go(func() {
			if err := d.stopInBackground(run, run.proxy); err != nil {
				log.Printf("proxy %s: %v", run.proxy.Name, err)
			}
		})
	}
	wg.Wait()
}

// commandProxy is the proxy named name, which must have a command.
func (d *daemon) commandProxy(name string) (config.Proxy, error) {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return config.Proxy{}, fmt.Errorf("the daemon has no configuration yet")
	}
	p, ok := cfg.FindProxy(name)
	switch {
	case !ok:
		return config.Proxy{}, fmt.Errorf("no proxy named %s", name)
	case p.Command == "":
		return config.Proxy{}, fmt.Errorf("%s has no command: Flock only passes its requests on — start its server yourself, or give it a command", name)
	}
	return p, nil
}

// proxySpec is the supervised process of a started proxy whose command runs in the foreground.
func (d *daemon) proxySpec(p config.Proxy) supervisor.Spec {
	path, args := shellCommand(p.Command)
	return supervisor.Spec{
		Name:       config.ProxyLog(p.Name),
		Path:       path,
		Args:       args,
		Env:        d.proxyEnv(p),
		Dir:        p.Dir,
		LogFile:    d.proxyLogFile(p.Name),
		StopSignal: syscall.SIGTERM,
		Group:      true, // a launcher's children (npm's dev server) hold the port
	}
}

// runProxyCommand runs one of a background proxy's commands to its end, appending its output to
// the proxy's log.
func (d *daemon) runProxyCommand(ctx context.Context, p config.Proxy, command string) error {
	if runningAsRoot() {
		return fmt.Errorf("Flock runs as root here, so it doesn't run %s's commands — run Flock as your own user", p.Name)
	}
	out, err := os.OpenFile(d.proxyLogFile(p.Name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	path, args := shellCommand(command)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = p.Dir
	cmd.Env = envutil.Merge(os.Environ(), d.proxyEnv(p))
	cmd.Stdout, cmd.Stderr = out, out
	supervisor.Configure(cmd)
	// Ended early (a timeout, or Stop while it starts), the whole command goes, not only the shell:
	// docker compose would go on. Once it finished, what it started in the background is left be.
	cmd.Cancel = func() error {
		supervisor.KillGroup(cmd)
		return nil
	}
	fmt.Fprintf(out, "\n[flock] %s running %s\n", time.Now().Format(time.RFC3339), command)
	err = cmd.Run()
	if err != nil {
		fmt.Fprintf(out, "[flock] %s failed: %v\n", command, err)
		return fmt.Errorf("`%s` failed: %v — see `flock logs %s`", command, err, config.ProxyLog(p.Name))
	}
	fmt.Fprintf(out, "[flock] %s finished\n", command)
	return nil
}

func (d *daemon) proxyLogFile(name string) string {
	return filepath.Join(d.root, "log", config.ProxyLog(name)+".log")
}

// proxyEnv is what a proxy's command gets on top of the daemon's environment: Flock's tools first
// on PATH (its PHP and Node.js shims, which pick the project's versions, and global npm tools),
// and PORT when the upstream is a port of this machine, as most dev servers read it.
func (d *daemon) proxyEnv(p config.Proxy) []string {
	bins := []string{filepath.Join(d.root, "bin"), node.GlobalBin(node.CurrentGlobalDir(d.root)), os.Getenv("PATH")}
	env := []string{"PATH=" + strings.Join(bins, string(os.PathListSeparator)), "BROWSER=none"}
	if u, err := url.Parse(p.Upstream); err == nil && u.Port() != "" && isLoopback(u.Hostname()) {
		env = append(env, "PORT="+u.Port())
	}
	return env
}

// shellCommand runs command through the shell, as a script's line would be.
func shellCommand(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/s", "/c", command}
	}
	return "/bin/sh", []string{"-c", command}
}

// liveProxyCommands brings the state of the proxies' commands up to date from what runs and what
// answers (Running, probed already).
func (d *daemon) liveProxyCommands(proxies []api.ProxyStatus) []api.ProxyStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, ps := range proxies {
		if ps.Command == nil {
			continue
		}
		c := *ps.Command
		run, started := d.proxyRuns[ps.Name]
		switch {
		case !started:
			c.State = api.ProxyStopped
		case ps.Running:
			c.State = api.ProxyRunning
		case run.problem != "":
			c.State, c.Problem = api.ProxyFailed, run.problem
		case c.StopCommand == "" && !d.procUp(c.Log):
			c.State, c.Problem = api.ProxyFailed, fmt.Sprintf("the command exited — see `flock logs %s`", c.Log)
		default:
			c.State = api.ProxyStarting
		}
		proxies[i].Command = &c
	}
	return proxies
}
