package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/ports"
	"flock/internal/supervisor"
)

// A Node.js site's dev server (`npm run dev`) runs from the site's first request on: the web server
// tells the daemon of every request (see web.WakeFunc), which starts the server when it doesn't run.

// Node.js sites get a loopback port from this range: the one their name hashes to while it's
// free, so a site keeps its port across restarts without it being stored.
const (
	firstNodePort = 22000
	nodePorts     = 1000
)

// nodeCommand runs the project's dev script; npm passes what follows -- to it.
const nodeCommand = "npm run dev -- --port "

// A dev server goes back to sleep once nothing has used it for nodeIdle: no request and no open
// connection (a tab with the app open keeps its hot-reload WebSocket), and no share. It's checked
// every nodeSleepCheck. Tests shorten both.
var (
	nodeIdle       = 30 * time.Minute
	nodeSleepCheck = time.Minute
)

// nodeRuns are the Node.js sites' dev servers, by site name.
type nodeRuns struct {
	mu     sync.Mutex
	specs  map[string]supervisor.Spec // every Node.js site's dev server, as of the last reload
	awake  map[string]bool            // started by a request (or the apps) and kept running
	ports  map[string]int             // the port each site got
	open   map[string]int             // requests in progress, open WebSockets included
	active map[string]time.Time       // when a request last came or ended
	// idleAfter is how long a server may go unused before it sleeps (see sleepNodeSites).
	idleAfter time.Duration
}

// wake marks a site's server as to run, and as used now. It reports whether it was asleep.
func (r *nodeRuns) wake(site string) bool {
	if r.awake == nil {
		r.awake, r.open, r.active = map[string]bool{}, map[string]int{}, map[string]time.Time{}
	}
	asleep := !r.awake[site]
	r.awake[site], r.active[site] = true, time.Now()
	return asleep
}

// nodeProcess is the name of a Node.js site's dev server and of its log.
func nodeProcess(site string) string { return "node-" + site }

// preferredNodePort is the port a site's name hashes to.
func preferredNodePort(site string) int {
	h := fnv.New32a()
	h.Write([]byte(site))
	return firstNodePort + int(h.Sum32()%nodePorts)
}

// nodePort gives a site its port: the one it has while its server runs or the port is free, else
// the first free one from the port its name hashes to. taken are the ports of Flock's services and
// of the sites handled before it in this reload. reloadMu is held.
func (d *daemon) nodePort(site string, taken map[int]bool) int {
	r := &d.node
	r.mu.Lock()
	current := r.ports[site]
	r.mu.Unlock()
	if current != 0 && !taken[current] && (d.procUp(nodeProcess(site)) || ports.Free("127.0.0.1", current)) {
		return current
	}
	start := preferredNodePort(site)
	for i := range nodePorts {
		port := firstNodePort + (start-firstNodePort+i)%nodePorts
		if !taken[port] && ports.Free("127.0.0.1", port) {
			return port
		}
	}
	return start
}

// nodeSpec is a Node.js site's dev server, on the Node.js version the site uses (node). Its whole
// tree is stopped with it: npm's child holds the port. The shims on PATH pick that version for npm.
func (d *daemon) nodeSpec(cfg *config.Config, site config.Site, port int, node string) supervisor.Spec {
	d.mu.Lock()
	restart := d.restarts[site.Name]
	d.mu.Unlock()
	// Only the site's own domain (and its subdomains) for Vite's allowed hosts: Vite before 8 takes
	// the variable as one name, so a list would match none. An alias needs server.allowedHosts.
	env := d.commandEnv([]string{cfg.Domain(site)}, "http://localhost:"+strconv.Itoa(port))
	// The version the shims pick, so a new one makes a new spec and restarts the server.
	env = append(env, "FLOCK_NODE="+node)
	if restart > 0 {
		env = append(env, "FLOCK_RESTART="+strconv.Itoa(restart)) // a new spec, so Ensure restarts it
	}
	command := nodeCommand + strconv.Itoa(port)
	// next dev listens on every interface unless told otherwise: past the web server's local-only
	// guard and a share's protections, to the whole network.
	if nextDev.MatchString(devScript(site.Path)) {
		command += " --hostname 127.0.0.1"
	}
	return supervisor.Spec{
		Name: nodeProcess(site.Name), Shell: command, Dir: site.Path, Tree: true, Env: env,
		LogFile: filepath.Join(d.root, "log", nodeProcess(site.Name)+".log"),
	}
}

// nextDev matches a dev script that runs Next.js's dev server.
var nextDev = regexp.MustCompile(`(^|[\s;&|(])next\s+dev\b`)

// devScript is the project's dev script, "" when it has none.
func devScript(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}
	return pkg.Scripts["dev"]
}

// hasDevScript reports whether the project's package.json has a dev script.
func hasDevScript(dir string) bool { return devScript(dir) != "" }

// keepNodeSites records the reload's Node.js sites, forgetting those that are gone.
func (r *nodeRuns) keep(specs map[string]supervisor.Spec, ports map[string]int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.awake {
		if _, ok := specs[name]; !ok {
			delete(r.awake, name)
		}
	}
	r.specs, r.ports = specs, ports
}

// isAwake reports whether a site's dev server is to run.
func (r *nodeRuns) isAwake(site string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.awake[site]
}

// wakeNode implements web.WakeFunc: it starts a Node.js site's dev server when it doesn't run.
// The web server calls it for every request, so it only starts the server in the background.
func (d *daemon) wakeNode(site string) (string, string, func()) {
	if runningAsRoot() {
		return api.NodeFailed, "Flock runs as root here, and doesn't run a project's dev server with root's rights", nil
	}
	r := &d.node
	r.mu.Lock()
	if _, known := r.specs[site]; !known {
		r.mu.Unlock()
		return "", "", nil
	}
	asleep := r.wake(site)
	r.open[site]++
	r.mu.Unlock()

	var once sync.Once
	done := func() {
		once.Do(func() {
			r.mu.Lock()
			r.open[site]--
			r.active[site] = time.Now()
			r.mu.Unlock()
		})
	}
	if asleep {
		go d.startNode(site)
		return api.NodeStarting, "", done
	}
	state, problem := d.nodeState(site, false)
	return state, problem, done
}

// sleepNodeSites puts the dev servers nothing used for idleAfter back to sleep, checking every
// check, until ctx ends. The daemon passes nodeIdle and nodeSleepCheck as they were when it
// started: a test's daemon that is still stopping mustn't read the next test's.
func (d *daemon) sleepNodeSites(ctx context.Context, idleAfter, check time.Duration) {
	d.node.mu.Lock()
	d.node.idleAfter = idleAfter
	d.node.mu.Unlock()
	tick := time.NewTicker(check)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		r := &d.node
		var idle []string
		r.mu.Lock()
		for site, awake := range r.awake {
			if awake && r.idle(site) && !d.shared(site) {
				idle = append(idle, site)
			}
		}
		r.mu.Unlock()
		for _, site := range idle {
			d.sleepNode(site, true)
		}
	}
}

// idle reports whether nothing has used a site's server for idleAfter. r.mu is held.
func (r *nodeRuns) idle(site string) bool {
	return r.idleAfter > 0 && r.open[site] == 0 && time.Since(r.active[site]) > r.idleAfter
}

// shared reports whether a site has a share running: its visitors come through the tunnel, which
// the dev server's activity doesn't see.
func (d *daemon) shared(site string) bool {
	return d.shares != nil && d.shares.Get(site) != nil
}

// sleepNode stops a site's dev server until its next request; whenIdle only while it's still
// idle (a request may have come since it was found idle).
func (d *daemon) sleepNode(site string, whenIdle bool) {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	r := &d.node
	r.mu.Lock()
	if whenIdle && !r.idle(site) {
		r.mu.Unlock()
		return
	}
	delete(r.awake, site)
	r.mu.Unlock()
	keep := map[string]bool{}
	for _, name := range d.procs.Running() {
		keep[name] = name != nodeProcess(site)
	}
	d.procs.Retain(keep)
}

// StopSite implements api.Backend: it puts a Node.js site's dev server to sleep now, until the
// site's next request.
func (d *daemon) StopSite(name string) error {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return fmt.Errorf("the daemon has no configuration yet")
	}
	site, ok := cfg.FindSite(name)
	switch {
	case !ok:
		return fmt.Errorf("%w: no site named %s", api.ErrInvalidRequest, name)
	case site.Kind() != config.TypeNode:
		return fmt.Errorf("%w: %s isn't a Node.js app: its server runs while Flock does", api.ErrInvalidRequest, name)
	}
	d.sleepNode(name, false)
	return nil
}

// startNode starts an awake site's dev server, unless a reload did already.
func (d *daemon) startNode(site string) {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	r := &d.node
	r.mu.Lock()
	spec, ok := r.specs[site]
	awake := r.awake[site]
	r.mu.Unlock()
	if ok && awake {
		d.procs.Ensure(spec)
	}
}

// nodeState is the state of a site's dev server; answering is whether its port answers (only
// asked for when that matters to the caller).
func (d *daemon) nodeState(site string, answering bool) (string, string) {
	if !d.node.isAwake(site) {
		return api.NodeSleeping, ""
	}
	for _, p := range d.procs.Status() {
		if p.Name != nodeProcess(site) {
			continue
		}
		switch {
		case !p.Up && p.Restarts > 0:
			return api.NodeFailed, nodeExitProblem(p.LastErr)
		case p.Up && answering:
			return api.NodeRunning, ""
		}
		return api.NodeStarting, ""
	}
	return api.NodeStarting, ""
}

// nodeExitProblem says why a dev server exited, from its process's last error.
func nodeExitProblem(lastErr string) string {
	lastErr = strings.TrimPrefix(lastErr, "<nil>")
	lastErr = strings.TrimPrefix(lastErr, ": ")
	if lastErr == "" {
		return "the dev server exited"
	}
	return lastErr
}

// liveNodeSites fills in the state of the Node.js sites' dev servers. The servers are copied:
// sites shares them with the last reload.
func (d *daemon) liveNodeSites(sites []api.SiteStatus) {
	for i, s := range sites {
		if s.NodeServer == nil {
			continue
		}
		n := *s.NodeServer
		n.State, n.Problem = d.nodeState(s.Name, answers("http://localhost:"+strconv.Itoa(n.Port)))
		if n.State == api.NodeFailed {
			if _, err := os.Stat(filepath.Join(s.Path, "node_modules")); err != nil {
				n.Problem = "node_modules is missing: run npm install in the project"
			}
		}
		if n.Problem == "" {
			n.Problem = s.NodeServer.Problem
		}
		sites[i].NodeServer = &n
	}
}

// nodeSiteProblem is what keeps a Node.js site from starting, found on reload.
func nodeSiteProblem(site config.Site) string {
	if runningAsRoot() {
		return "Flock runs as root here, and doesn't run a project's dev server with root's rights"
	}
	if !hasDevScript(site.Path) {
		return fmt.Sprintf("%s has no dev script in package.json for Flock to run", site.Name)
	}
	return ""
}
