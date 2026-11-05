package daemon

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
	running bool
	problem string // why the background command failed
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
	if run, ok := d.proxyRuns[name]; ok && run.running {
		d.mu.Unlock()
		return nil
	}
	run := &proxyRun{running: p.StopCommand != ""}
	if d.proxyRuns == nil {
		d.proxyRuns = map[string]*proxyRun{}
	}
	d.proxyRuns[name] = run
	d.mu.Unlock()

	if p.StopCommand == "" {
		return d.reload()
	}
	go func() {
		problem := ""
		if err := d.runProxyCommand(context.Background(), p, p.Command); err != nil {
			problem = err.Error()
		}
		d.mu.Lock()
		run.running, run.problem = false, problem
		d.mu.Unlock()
	}()
	return nil
}

// StopProxy implements api.Backend: it stops a foreground command, or runs the stop command of
// a background one.
func (d *daemon) StopProxy(name string) error {
	p, err := d.commandProxy(name)
	if err != nil {
		return err
	}
	d.mu.Lock()
	delete(d.proxyRuns, name)
	d.mu.Unlock()

	d.procs.Stop(config.ProxyLog(name))
	if p.StopCommand == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopCommandTimeout)
	defer cancel()
	return d.runProxyCommand(ctx, p, p.StopCommand)
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
	log, err := os.OpenFile(d.proxyLogFile(p.Name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer log.Close()

	path, args := shellCommand(command)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = p.Dir
	cmd.Env = envutil.Merge(os.Environ(), d.proxyEnv(p))
	cmd.Stdout, cmd.Stderr = log, log
	supervisor.Configure(cmd)
	fmt.Fprintf(log, "\n[flock] %s running %s\n", time.Now().Format(time.RFC3339), command)
	err = cmd.Run()
	if err != nil {
		fmt.Fprintf(log, "[flock] %s failed: %v\n", command, err)
		return fmt.Errorf("`%s` failed: %v — see `flock logs %s`", command, err, config.ProxyLog(p.Name))
	}
	fmt.Fprintf(log, "[flock] %s finished\n", command)
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
