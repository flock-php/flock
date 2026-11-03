package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/node"
	"flock/internal/ports"
	"flock/internal/supervisor"
)

// nodeScript is the package.json script a Node.js site runs.
const nodeScript = "dev"

// nodeSite is how a Node.js site is served: its dev server's process, when it can run, and its state.
type nodeSite struct {
	spec     *supervisor.Spec
	upstream string
	status   api.SiteNodeServer
}

// nodeSite prepares a Node.js site: `npm run dev -- --port <port>` in its folder, with the site's
// Node.js (from .flockrc, .nvmrc or .node-version, an isolation, or the default) and PORT set, so
// Next.js, Nuxt and Vite all listen where the web server proxies to. The upstream is localhost:
// Vite listens there, which may be IPv6 only. Vite also only answers hosts it knows, told through
// __VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS.
func (d *daemon) nodeSite(cfg *config.Config, site config.Site, spec string, hosts []string) nodeSite {
	name := config.NodePortID(site.Name)
	ns := nodeSite{
		upstream: "http://localhost:" + strconv.Itoa(site.Port),
		status:   api.SiteNodeServer{Port: site.Port, Log: name},
	}
	fail := func(state, format string, args ...any) nodeSite {
		ns.status.State, ns.status.Problem = state, fmt.Sprintf(format, args...)
		return ns
	}

	if site.Port == 0 {
		return fail(api.NodeFailed, "the site has no port for its dev server — switch it to Node.js again")
	}
	script := ""
	if site.Command == "" {
		if script = packageScript(site.Path, nodeScript); script == "" {
			return fail(api.NodeFailed, "%s has no %q script in package.json — add one, or give the site a command of its own", site.Path, nodeScript)
		}
	}
	_, version, err := node.Resolve(d.root, spec)
	if err != nil {
		return fail(api.NodeUnavailable, "%v", err)
	}
	ns.status.Version = version
	if other, taken := cfg.UsedPorts("")[site.Port]; taken && other != name {
		return fail(api.NodeFailed, "port %d is also configured for %s — switch the site to Node.js again for a new port", site.Port, serviceName(other))
	}

	port := strconv.Itoa(site.Port)
	var path string
	var args, env []string
	if site.Command != "" {
		path, args, env, err = d.customCommand(version, site.Path, strings.ReplaceAll(site.Command, "{port}", port))
		ns.status.Command, ns.status.Custom = strings.ReplaceAll(site.Command, "{port}", port), true
	} else {
		ns.status.Script = nodeScript
		npmArgs := []string{"run", nodeScript, "--", "--port", port}
		// next dev listens on every interface, so the LAN could reach it past the web server
		// (which answers this machine only); Vite and Nuxt listen on localhost already.
		if first, _, _ := strings.Cut(strings.TrimSpace(script), " "); first == "next" {
			npmArgs = append(npmArgs, "--hostname", "127.0.0.1")
		}
		ns.status.Command = "npm " + strings.Join(npmArgs, " ")
		path, args, env, err = node.NpmCommand(d.root, version, npmArgs...)
	}
	if err != nil {
		return fail(api.NodeFailed, "%v", err)
	}
	d.mu.Lock()
	restart := d.restarts[site.Name]
	d.mu.Unlock()
	env = append(env, "PORT="+port, "BROWSER=none", "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS="+strings.Join(hosts, ","),
		// Changes when the site is restarted, so the supervisor starts it again.
		"FLOCK_RESTART="+strconv.Itoa(restart))
	s := supervisor.Spec{
		Name:       name,
		Path:       path,
		Args:       args,
		Env:        env,
		Dir:        site.Path,
		LogFile:    filepath.Join(d.root, "log", name+".log"),
		StopSignal: syscall.SIGTERM,
		Group:      true, // npm's children (the dev server itself) hold the port
	}
	if !d.procUp(s.Name) && !ports.Free("127.0.0.1", site.Port) {
		return fail(api.NodeFailed, "%s", ports.Describe(site.Port))
	}
	ns.spec = &s
	ns.status.State = api.NodeStarting
	return ns
}

// customCommand runs a site's own dev server command through the shell in dir, with the site's
// Node.js and npm's global prefix as for npm, and the project's node_modules/.bin first on PATH, as
// `npm run` has it (so "next dev --port {port}" works as it does in a script).
func (d *daemon) customCommand(version, dir, command string) (path string, args, env []string, err error) {
	_, _, env, err = node.NpmCommand(d.root, version)
	if err != nil {
		return "", nil, nil, err
	}
	bin := filepath.Join(dir, "node_modules", ".bin")
	for i, e := range env {
		if rest, ok := strings.CutPrefix(e, "PATH="); ok {
			env[i] = "PATH=" + bin + string(os.PathListSeparator) + rest
		}
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/d", "/s", "/c", command}, env, nil
	}
	return "/bin/sh", []string{"-c", command}, env, nil
}

// packageScript is a script of dir's package.json ("" when there's none).
func packageScript(dir, name string) string {
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
	return pkg.Scripts[name]
}

// liveNodeServers brings the state of Node.js sites' dev servers up to date: one that was started
// is running once it answers, and failed once its process is down.
func (d *daemon) liveNodeServers(sites []api.SiteStatus) []api.SiteStatus {
	var out []api.SiteStatus
	for i, s := range sites {
		ns := s.NodeServer
		if ns == nil || (ns.State != api.NodeStarting && ns.State != api.NodeRunning) {
			continue
		}
		if out == nil {
			out = append([]api.SiteStatus(nil), sites...)
		}
		live := *ns
		running, _ := d.probe.get(d.root, "http://localhost:"+strconv.Itoa(ns.Port))
		switch {
		case running:
			live.State = api.NodeRunning
		case !d.procUp(ns.Log):
			live.State, live.Problem = api.NodeFailed, fmt.Sprintf("%s exited — see `flock logs %s`", nodeScript, ns.Log)
		default:
			live.State = api.NodeStarting
		}
		out[i].NodeServer = &live
	}
	if out == nil {
		return sites
	}
	return out
}
