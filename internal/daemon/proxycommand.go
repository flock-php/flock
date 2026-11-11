package daemon

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/envutil"
	"flock/internal/node"
	"flock/internal/supervisor"
)

// A proxy's command (see config.Proxy.Command) runs only after the proxy is started, from the
// apps, `flock proxy start` or the API: the user decides when a proxy's server runs. What was
// started isn't kept across daemon restarts.

// proxyStartGrace is how long a started server may take before Flock says nothing answers yet.
const proxyStartGrace = time.Minute

// backgroundTimeout bounds a background proxy's start or stop command (`docker compose up -d`
// may pull images first).
const backgroundTimeout = 10 * time.Minute

// proxyRuns is what Flock started of the proxies' commands, by proxy name.
type proxyRuns struct {
	mu      sync.Mutex
	started map[string]time.Time // started and not stopped since, by when; zero once seen running
	busy    map[string]string    // a background command that runs: ProxyStarting or ProxyStopping
	failed  map[string]string    // why the last command failed
}

func (r *proxyRuns) init() {
	if r.started == nil {
		r.started, r.busy, r.failed = map[string]time.Time{}, map[string]string{}, map[string]string{}
	}
}

// isStarted reports whether the proxy was started and not stopped since.
func (r *proxyRuns) isStarted(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.started[name]
	return ok
}

// retain forgets the proxies not in keep, so a proxy added again under the name doesn't start.
func (r *proxyRuns) retain(keep map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	for name := range r.started {
		if !keep[name] {
			delete(r.started, name)
		}
	}
	for name := range r.failed {
		if !keep[name] {
			delete(r.failed, name)
		}
	}
}

// proxyProcess is the name of a proxy's process and of its commands' log.
func proxyProcess(name string) string { return "proxy-" + name }

// proxyCommandStatus is a proxy's command as the reload sees it; its state is worked out when the
// status is read (see liveProxyCommands).
func proxyCommandStatus(p config.Proxy) *api.ProxyCommand {
	if p.Command == "" {
		return nil
	}
	return &api.ProxyCommand{Command: p.Command, Dir: p.Dir, StopCommand: p.StopCommand, State: api.ProxyStopped, Log: proxyProcess(p.Name)}
}

// commandEnv is the environment Flock runs a server's command with: its shims and the default
// Node.js version's global packages first on PATH (so the project's PHP and Node.js are used),
// PORT when the server is expected on a port of this machine, the domains for Vite's allowed hosts
// (it refuses other Host headers; ".app.test" allows app.test and its subdomains), and Flock's CA
// for Node.js's HTTPS requests to other sites.
func (d *daemon) commandEnv(domains []string, upstream string) []string {
	path := strings.Join([]string{filepath.Join(d.root, "bin"), node.GlobalBin(node.CurrentGlobalDir(d.root)), os.Getenv("PATH")},
		string(os.PathListSeparator))
	hosts := make([]string, len(domains))
	for i, domain := range domains {
		hosts[i] = "." + domain
	}
	env := []string{"PATH=" + path, "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=" + strings.Join(hosts, ",")}
	if u, err := url.Parse(upstream); err == nil && u.Port() != "" && isLoopback(u.Hostname()) {
		env = append(env, "PORT="+u.Port())
	}
	if ca := filepath.Join(d.root, "certs", "ca.crt"); exists(ca) {
		env = append(env, "NODE_EXTRA_CA_CERTS="+ca)
	}
	return env
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// proxySpec is the process of a proxy whose command is its server. Its whole tree is stopped
// with it: `npm run dev`'s child holds the port.
func (d *daemon) proxySpec(cfg *config.Config, p config.Proxy) supervisor.Spec {
	return supervisor.Spec{
		Name: proxyProcess(p.Name), Shell: p.Command, Dir: p.Dir, Tree: true,
		Env:     d.commandEnv([]string{cfg.ProxyDomain(p)}, p.Upstream),
		LogFile: filepath.Join(d.root, "log", proxyProcess(p.Name)+".log"),
	}
}

// commandProxy finds a proxy with a command.
func (d *daemon) commandProxy(name string) (*config.Config, config.Proxy, error) {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return nil, config.Proxy{}, fmt.Errorf("the daemon has no configuration yet")
	}
	p, ok := cfg.FindProxy(name)
	switch {
	case !ok:
		return nil, p, fmt.Errorf("%w: no proxy named %s", api.ErrInvalidRequest, name)
	case p.Command == "":
		return nil, p, fmt.Errorf("%w: %s has no command â€” give it the command that starts its server", api.ErrInvalidRequest, name)
	}
	return cfg, p, nil
}

// StartProxy implements api.Backend.
func (d *daemon) StartProxy(name string) error {
	cfg, p, err := d.commandProxy(name)
	if err != nil {
		return err
	}
	if info, err := os.Stat(p.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: %s's folder %s doesn't exist", api.ErrInvalidRequest, name, p.Dir)
	}
	if runningAsRoot() {
		return fmt.Errorf("%w: Flock runs as root here, and doesn't run a project's commands with root's rights", api.ErrConflict)
	}

	r := &d.runs
	r.mu.Lock()
	r.init()
	if busy := r.busy[name]; busy != "" {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s is %s", api.ErrConflict, name, busy)
	}
	_, started := r.started[name]
	r.mu.Unlock()
	if started && !p.Background() {
		return nil
	}
	if !d.procUp(proxyProcess(name)) && answers(p.Upstream) {
		return fmt.Errorf("%w: something already answers at %s â€” stop it first", api.ErrConflict, p.Upstream)
	}

	r.mu.Lock()
	// Again under the lock that claims the start: another start may have come in while the
	// upstream was probed.
	if busy := r.busy[name]; busy != "" {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s is %s", api.ErrConflict, name, busy)
	}
	r.started[name] = time.Now()
	delete(r.failed, name)
	if p.Background() {
		r.busy[name] = api.ProxyStarting
	}
	r.mu.Unlock()
	d.probe.forget(p.Upstream)

	if p.Background() {
		go d.runBackground(cfg, p, p.Command)
		return nil
	}
	return d.reload()
}

// StopProxy implements api.Backend. A server Flock didn't start is only stopped by a stop command.
func (d *daemon) StopProxy(name string) error {
	cfg, p, err := d.commandProxy(name)
	if err != nil {
		return err
	}
	if p.Background() && runningAsRoot() {
		return fmt.Errorf("%w: Flock runs as root here, and doesn't run a project's commands with root's rights", api.ErrConflict)
	}

	r := &d.runs
	r.mu.Lock()
	r.init()
	if busy := r.busy[name]; busy != "" {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s is %s", api.ErrConflict, name, busy)
	}
	delete(r.started, name)
	delete(r.failed, name)
	if p.Background() {
		r.busy[name] = api.ProxyStopping
	}
	r.mu.Unlock()
	defer d.probe.forget(p.Upstream)

	if p.Background() {
		go d.runBackground(cfg, p, p.StopCommand)
		return nil
	}
	return d.reload() // stops the process: it isn't kept any more
}

// runBackground runs a background proxy's start or stop command to its end, its output in the
// proxy's log. Whatever it starts outlives it, and the daemon.
func (d *daemon) runBackground(cfg *config.Config, p config.Proxy, command string) {
	err := d.runCommand(cfg, p, command)

	r := &d.runs
	r.mu.Lock()
	if r.busy[p.Name] == api.ProxyStarting && err != nil {
		delete(r.started, p.Name)
	}
	delete(r.busy, p.Name)
	if err != nil {
		r.failed[p.Name] = err.Error()
	}
	r.mu.Unlock()
	d.probe.forget(p.Upstream)
}

func (d *daemon) runCommand(cfg *config.Config, p config.Proxy, command string) error {
	logPath := filepath.Join(d.root, "log", proxyProcess(p.Name)+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()

	ctx, cancel := context.WithTimeout(context.Background(), backgroundTimeout)
	defer cancel()
	cmd := supervisor.ShellCommand(command)
	cmd.Dir, cmd.Stdout, cmd.Stderr = p.Dir, log, log
	cmd.Env = envutil.Merge(os.Environ(), d.commandEnv([]string{cfg.ProxyDomain(p)}, p.Upstream))
	supervisor.Configure(cmd)

	fmt.Fprintf(log, "\n[flock] %s $ %s\n", time.Now().Format(time.RFC3339), command)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(log, "[flock] start failed: %v\n", err)
		return fmt.Errorf("`%s` couldn't run: %v", command, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// The shell and what it started: a command that never finished doesn't leave a server.
		supervisor.KillTree(cmd)
		<-done
		err = fmt.Errorf("it didn't finish in %s", backgroundTimeout)
	}
	if err != nil {
		fmt.Fprintf(log, "[flock] failed: %v\n", err)
		return fmt.Errorf("`%s` failed: %v â€” see its output", command, err)
	}
	fmt.Fprintf(log, "[flock] done\n")
	return nil
}

// liveProxyCommands fills in the state of the proxies' commands, from their processes and what
// answers at their upstreams (Running, probed already). The commands are copied: proxies shares
// them with the last reload.
func (d *daemon) liveProxyCommands(proxies []api.ProxyStatus) {
	procs := map[string]supervisor.ProcStatus{}
	for _, p := range d.procs.Status() {
		procs[p.Name] = p
	}

	r := &d.runs
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	for i, ps := range proxies {
		if ps.Command == nil {
			continue
		}
		c := *ps.Command
		since, started := r.started[ps.Name]
		switch {
		case r.busy[ps.Name] != "":
			c.State = r.busy[ps.Name]
		case r.failed[ps.Name] != "":
			c.State, c.Problem = api.ProxyFailed, r.failed[ps.Name]
		case c.StopCommand != "":
			// A background server runs on its own: it's running whenever it answers.
			switch {
			case ps.Running:
				c.State = api.ProxyRunning
				if started {
					r.started[ps.Name] = time.Time{}
				}
			case started && !since.IsZero():
				c.State = api.ProxyStarting
				if time.Since(since) > proxyStartGrace {
					c.Problem = fmt.Sprintf("nothing answers at %s yet â€” does the command start the server there?", ps.Upstream)
				}
			default:
				delete(r.started, ps.Name)
			}
		case !started:
		default:
			proc, ok := procs[proxyProcess(ps.Name)]
			switch {
			case !ok, !proc.Up && proc.Restarts == 0: // not started yet
				c.State = api.ProxyStarting
			case !proc.Up:
				c.State, c.Problem = api.ProxyFailed, exitProblem(proc.LastErr)
			case ps.Running:
				c.State = api.ProxyRunning
			default:
				c.State = api.ProxyStarting
				if time.Since(since) > proxyStartGrace {
					c.Problem = fmt.Sprintf("nothing answers at %s yet â€” does the command start the server there?", ps.Upstream)
				}
			}
		}
		proxies[i].Command = &c
	}
}

// exitProblem says why a proxy's server exited, from its process's last error ("exit status 1:
// <its last line>"). One that exits without an error most likely started the server in the
// background, which needs a stop command.
func exitProblem(lastErr string) string {
	if rest, ok := strings.CutPrefix(lastErr, "<nil>"); ok {
		return "the command exited" + rest + " â€” if it starts the server in the background, give the proxy a stop command"
	}
	return "the command exited: " + lastErr
}
