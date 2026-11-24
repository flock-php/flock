package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/url"
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
const (
	devCommand  = "npm run dev"
	nodeCommand = devCommand + " -- --port "
)

// A dev server goes back to sleep once nothing has used it for nodeIdle: no request and no open
// connection (a tab with the app open keeps its hot-reload WebSocket), and no share. It's checked
// every nodeSleepCheck. Tests shorten both.
var (
	nodeIdle       = 30 * time.Minute
	nodeSleepCheck = time.Minute
)

// nodeRuns are the Node.js sites' dev servers, by site name.
type nodeRuns struct {
	mu      sync.Mutex
	specs   map[string]supervisor.Spec // every Node.js site's dev server, as of the last reload
	awake   map[string]bool            // started by a request (or the apps) and kept running
	ports   map[string]int             // the port each site got
	open    map[string]int             // requests in progress, open WebSockets included
	active  map[string]time.Time       // when a request last came or ended
	devOnly map[string]bool            // PHP sites' dev servers: the browser reaches them directly, see sleepNodeSites
	woke    map[string]time.Time       // when a sleeping server was woken, for how long a request waits for it
	// idleAfter is how long a server may go unused before it sleeps (see sleepNodeSites).
	idleAfter time.Duration
}

// wake marks a site's server as to run, and as used now. It reports whether it was asleep.
func (r *nodeRuns) wake(site string) bool {
	if r.awake == nil {
		r.awake, r.open, r.active, r.woke = map[string]bool{}, map[string]int{}, map[string]time.Time{}, map[string]time.Time{}
	}
	asleep := !r.awake[site]
	r.awake[site], r.active[site] = true, time.Now()
	if asleep {
		r.woke[site] = time.Now()
	}
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
// ownPort is set when the project fixes its dev server's port itself, which Flock then leaves alone.
func (d *daemon) nodeSpec(cfg *config.Config, site config.Site, port int, node string, ownPort bool) supervisor.Spec {
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
	if ownPort {
		command = devCommand
	}
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
func (r *nodeRuns) keep(specs map[string]supervisor.Spec, ports map[string]int, devOnly map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.awake {
		if _, ok := specs[name]; !ok {
			delete(r.awake, name)
		}
	}
	r.specs, r.ports, r.devOnly = specs, ports, devOnly
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
	devOnly := r.devOnly[site]
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
	if asleep && devOnly {
		// PHP renders @vite as soon as it runs, and needs the server's public/hot by then.
		if !d.startNode(site) {
			d.awaitDevServer(site)
		}
		return api.NodeStarting, "", done
	}
	if asleep {
		go d.startNode(site)
		return api.NodeStarting, "", done
	}
	if devOnly {
		// The server of the project may have stopped since the wake left it to it: Flock's takes over.
		outside := false
		if !d.procUp(nodeProcess(site)) {
			outside = d.startNode(site)
		}
		if !outside {
			d.awaitDevServer(site)
		}
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
			if d.browserConnected(site) {
				continue
			}
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

// hasDevServer reports whether a PHP site runs a dev server of its own (see phpDevServer).
func (r *nodeRuns) hasDevServer(site string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.devOnly[site]
}

// StopSite implements api.Backend: it puts a site's dev server to sleep now, until the
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
	case site.Kind() != config.TypeNode && !d.node.hasDevServer(name):
		return fmt.Errorf("%w: %s has no dev server for Flock to stop: its server runs while Flock does", api.ErrInvalidRequest, name)
	}
	d.sleepNode(name, false)
	return nil
}

// startNode starts an awake site's dev server, unless a reload did already. It reports whether a
// PHP site's Vite runs without Flock, which then does the work.
func (d *daemon) startNode(site string) (outside bool) {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	r := &d.node
	r.mu.Lock()
	spec, ok := r.specs[site]
	awake, devOnly := r.awake[site], r.devOnly[site]
	r.mu.Unlock()
	if !ok || !awake {
		return false
	}
	if devOnly && d.outsideDevServer(site, spec.Dir) != 0 {
		return true
	}
	if d.procs.Ensure(spec) {
		r.started(site)
	}
	return false
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
		if s.NodeServer != nil {
			sites[i].NodeServer = d.liveDevServer(s, s.NodeServer)
		}
		if s.DevServer != nil {
			n := d.liveDevServer(s, s.DevServer)
			// A Vite run without Flock has no process of its own: it runs where its plugin says.
			if port := d.outsideDevServer(s.Name, s.Path); port != 0 {
				n.State, n.Problem, n.Port = api.NodeRunning, "", port
			}
			sites[i].DevServer = n
		}
	}
}

// liveDevServer is a copy of a site's dev server status with its state now.
func (d *daemon) liveDevServer(s api.SiteStatus, server *api.SiteNodeServer) *api.SiteNodeServer {
	n := *server
	n.State, n.Problem = d.nodeState(s.Name, answers("http://localhost:"+strconv.Itoa(n.Port)))
	if n.State == api.NodeFailed {
		if _, err := os.Stat(filepath.Join(s.Path, "node_modules")); err != nil {
			n.Problem = "node_modules is missing: run npm install in the project"
		}
	}
	if n.Problem == "" {
		n.Problem = server.Problem
	}
	return &n
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

// devOrigin is where a Vite plugin says its dev server listens: the scheme and host it advertises
// (an HTTPS certificate is checked against that name), the port, and the base path the server
// serves under ("/" unless the project sets one).
type devOrigin struct{ scheme, host, port, base string }

// externalDevServer is the port of a PHP site's Vite that runs without Flock: one that answers where
// the project's Vite plugin says it listens (see devServerOrigins). 0 when there is none.
func externalDevServer(dir string) int {
	for _, o := range devServerOrigins(dir) {
		if isVite(o) {
			port, _ := strconv.Atoi(o.port)
			return port
		}
	}
	return 0
}

// outsideDevServer is the port of a PHP site's Vite that runs without Flock, 0 when none does or
// when the Vite found is Flock's own (its process is up). Flock then leaves it alone, so two servers
// don't fight for the port, even when the project fixes the port both would use.
func (d *daemon) outsideDevServer(site, dir string) int {
	if d.procUp(nodeProcess(site)) {
		return 0
	}
	return externalDevServer(dir)
}

// devServerOrigins are where a project's Vite plugin says its dev server listens: public/hot, which
// laravel-vite-plugin writes, and the viteServer origin (with the base path) in the entrypoints.json
// vite-plugin-symfony writes in its build directory (public/<build>/ or public/<build>/.vite/).
// They can be stale, so what listens there is checked.
func devServerOrigins(dir string) []devOrigin {
	var origins []devOrigin
	add := func(origin, base string) {
		u, err := url.Parse(strings.TrimSpace(origin))
		if err != nil || u.Port() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		if base == "" {
			base = "/"
		}
		base = "/" + strings.Trim(base, "/")
		if base != "/" {
			base += "/"
		}
		origins = append(origins, devOrigin{scheme: u.Scheme, host: u.Hostname(), port: u.Port(), base: base})
	}
	if data, err := os.ReadFile(filepath.Join(dir, "public", "hot")); err == nil {
		add(string(data), "/")
	}
	for _, pattern := range []string{"entrypoints.json", filepath.Join(".vite", "entrypoints.json")} {
		files, _ := filepath.Glob(filepath.Join(dir, "public", "*", pattern))
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			// viteServer is false once the files are built; while Vite runs, a URL string in the
			// plugin's newer versions and an object holding the origin in older ones.
			var entrypoints struct {
				Base       string          `json:"base"`
				ViteServer json.RawMessage `json:"viteServer"`
			}
			if json.Unmarshal(data, &entrypoints) != nil {
				continue
			}
			var origin string
			if json.Unmarshal(entrypoints.ViteServer, &origin) != nil {
				var object struct {
					Origin string `json:"origin"`
				}
				if json.Unmarshal(entrypoints.ViteServer, &object) != nil {
					continue
				}
				origin = object.Origin
			}
			if origin != "" {
				add(origin, entrypoints.Base)
			}
		}
	}
	return origins
}

// isVite reports whether Vite's dev server answers where its plugin says (over the scheme and host
// it advertises, with an HTTPS certificate checked), under its base path: it serves its client
// script at <base>@vite/client, a script that logs "[vite]". A stale public/hot, with some other
// program on its port, doesn't count, even one that answers 200 to everything.
func isVite(o devOrigin) bool {
	host := net.JoinHostPort(o.host, o.port)
	if !answers("http://" + host) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.scheme+"://"+host+o.base+"@vite/client", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	script, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<17))
	return strings.Contains(string(script), "[vite]")
}

// browserConnected reports whether a PHP site's dev server has a browser connected (an open tab's
// hot-reload WebSocket goes straight to it, past Flock's web server), and counts that as use.
func (d *daemon) browserConnected(site string) bool {
	r := &d.node
	r.mu.Lock()
	port, devOnly := r.ports[site], r.devOnly[site]
	r.mu.Unlock()
	if !devOnly || ports.Connections(port) == 0 {
		return false
	}
	r.mu.Lock()
	r.active[site] = time.Now()
	r.mu.Unlock()
	return true
}

// started records that a new process runs a site's dev server: requests to a PHP site wait for it to
// answer (see awaitDevServer), as after the wake.
func (r *nodeRuns) started(site string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.woke == nil {
		r.woke = map[string]time.Time{}
	}
	r.woke[site] = time.Now()
}

// ensureIfAwake keeps a dev server running when a request woke it (see wakeNode), and not when
// Flock runs as root, or when a PHP site's (devOnly) already runs without Flock.
func (d *daemon) ensureIfAwake(site string, spec supervisor.Spec, devOnly bool, keep map[string]bool) {
	if devOnly && d.outsideDevServer(site, spec.Dir) != 0 {
		return // the project's own server does the work
	}
	if d.node.isAwake(site) && !runningAsRoot() {
		if d.procs.Ensure(spec) {
			d.node.started(site) // started, or replaced by a new spec: PHP waits for it again
		}
		keep[spec.Name] = true
	}
}

// phpDevServer is the dev server a PHP site runs beside PHP: Vite, when the project's dev script
// is just `vite`, on the port Flock gives it, or on the one the project's vite.config fixes. ok is
// false for any other site. takenPorts gets the port. reloadMu is held.
func (d *daemon) phpDevServer(cfg *config.Config, site config.Site, node string, takenPorts map[int]bool) (spec supervisor.Spec, port int, ok bool) {
	if !isViteDev(devScript(site.Path)) {
		return spec, 0, false
	}
	// A port another site already fixed or got isn't taken twice, nor one another program listens
	// on (Vite would move to the next one, and PHP find public/hot elsewhere): Flock gives this one
	// its own. Its own process there is no other program.
	port = viteConfigPort(site.Path)
	ownPort := port != 0 && !takenPorts[port] &&
		(d.procUp(nodeProcess(site.Name)) || ports.Free("127.0.0.1", port))
	if !ownPort {
		port = d.nodePort(site.Name, takenPorts)
	}
	takenPorts[port] = true
	return d.nodeSpec(cfg, site, port, node, ownPort), port, true
}

// viteDev matches a dev script that is only Vite's dev server, possibly with flags: anything else
// (`vite build`, `concurrently …`, `mix`) can't be given a --port.
var viteDev = regexp.MustCompile(`^\s*(npx\s+)?vite(\s+(--?[\w-]+([ =][\w./:-]+)?\s*)*)?$`)

// isViteDev reports whether a project's dev script runs Vite's dev server.
func isViteDev(script string) bool {
	if script == "" || regexp.MustCompile(`\b(build|preview|optimize)\b`).MatchString(script) {
		return false
	}
	return viteDev.MatchString(script)
}

// devWait is how long a request to a PHP site waits for its dev server to answer after waking it.
// Tests shorten it.
var devWait = 15 * time.Second

// awaitDevServer holds a request to a PHP site until its dev server answers (so public/hot exists
// when PHP renders @vite), it fails, or devWait has passed since it was woken. Callers skip it when
// the project's own Vite runs (see startNode): it isn't Flock's to wait for.
func (d *daemon) awaitDevServer(site string) {
	r := &d.node
	r.mu.Lock()
	port, woke := r.ports[site], r.woke[site]
	r.mu.Unlock()
	if port == 0 {
		return
	}
	for time.Since(woke) < devWait {
		if answers("http://localhost:" + strconv.Itoa(port)) {
			return
		}
		if state, _ := d.nodeState(site, false); state == api.NodeFailed || !d.node.isAwake(site) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// vitePort matches a port set in the body of a Vite config's server block.
var vitePort = regexp.MustCompile(`\bport\s*:\s*(\d{2,5})\b`)

// serverBlock matches where a Vite config's server options start.
var serverBlock = regexp.MustCompile(`\bserver\s*:\s*\{`)

// jsCode is JavaScript source without its comments and with the contents of its strings emptied,
// so a "//" in a URL isn't a comment, and braces and `port:` are only found in code.
func jsCode(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return b.String()
			}
			i += end + 4
		case c == '"' || c == '\'' || c == '`':
			b.WriteByte(c)
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// viteConfigPort is the server port a project's vite.config fixes in its `server` options, 0 when
// it doesn't. A port nested deeper (hmr, proxy) or in a comment isn't the server's.
func viteConfigPort(dir string) int {
	for _, ext := range []string{"js", "ts", "mjs", "mts", "cjs", "cts"} {
		data, err := os.ReadFile(filepath.Join(dir, "vite.config."+ext))
		if err != nil {
			continue
		}
		src := jsCode(string(data))
		loc := serverBlock.FindStringIndex(src)
		if loc == nil {
			return 0
		}
		// The block's own properties: what is at depth 1 between its braces.
		var own strings.Builder
		depth := 0
		for _, c := range src[loc[1]-1:] {
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
			if depth == 0 {
				break
			}
			if depth == 1 && c != '{' {
				own.WriteRune(c)
			}
		}
		if m := vitePort.FindStringSubmatch(own.String()); m != nil {
			port, _ := strconv.Atoi(m[1])
			return port
		}
		return 0
	}
	return 0
}
