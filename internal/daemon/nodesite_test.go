package daemon

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
	"flock/internal/supervisor"
)

// fakeNpm puts an npm into Flock's bin folder (first on the dev server's PATH) that logs its
// arguments and runs the test binary in mode (see testCommand).
func fakeNpm(t *testing.T, root, mode string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		script := "@echo npm %*\r\n@\"" + os.Args[0] + "\" " + mode + "\r\n"
		if err := os.WriteFile(filepath.Join(bin, "npm.cmd"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	script := "#!/bin/sh\necho npm \"$@\"\nexec \"" + os.Args[0] + "\" " + mode + "\n"
	if err := os.WriteFile(filepath.Join(bin, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// nodeApp is a parked folder with a Node.js app in it.
func nodeApp(t *testing.T, c *config.Config) {
	t.Helper()
	parked := t.TempDir()
	if err := os.Mkdir(filepath.Join(parked, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(parked, "app", "package.json"), `{"scripts": {"dev": "vite"}}`)
	c.Parked = []string{parked}
}

func siteStatus(t *testing.T, client *api.Client, name string) api.SiteStatus {
	t.Helper()
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st.Sites {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no site %s", name)
	return api.SiteStatus{}
}

func getPage(t *testing.T, cfg *config.Config, host string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("X-Flock-Page"), string(body)
}

func TestANodeSiteStartsOnItsFirstRequest(t *testing.T) {
	root, cfg := testRoot(t, func(c *config.Config) { nodeApp(t, c) })
	fakeNpm(t, root, "flock-test-server")
	client := startDaemon(t, root)

	s := siteStatus(t, client, "app")
	if s.Type != config.TypeNode || s.NodeServer == nil || s.NodeServer.State != api.NodeSleeping || s.NodeServer.Log != "node-app" {
		t.Fatalf("before the first request: %+v %+v", s, s.NodeServer)
	}
	port := s.NodeServer.Port
	if port < firstNodePort || port >= firstNodePort+nodePorts || s.NodeServer.Command != "npm run dev -- --port "+strconv.Itoa(port) {
		t.Fatalf("port %d, command %q", port, s.NodeServer.Command)
	}

	// The first request starts the dev server and gets the page that waits for it.
	code, page, body := getPage(t, cfg, "app.test")
	if code != http.StatusServiceUnavailable || page != "node-starting" || !strings.Contains(body, "Starting the dev server") {
		t.Fatalf("first request: %d %q\n%s", code, page, body)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		code, _, body = getPage(t, cfg, "app.test")
		if code == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the dev server didn't answer: %d\n%s\nlog:\n%s", code, body, readLog(t, root, "node-app"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if body != "dev server for app.test" {
		t.Fatalf("got %q", body)
	}
	if s := siteStatus(t, client, "app"); s.NodeServer.State != api.NodeRunning || s.NodeServer.Port != port {
		t.Fatalf("after it answered: %+v", s.NodeServer)
	}
	if log := readLog(t, root, "node-app"); !strings.Contains(log, "npm run dev -- --port "+strconv.Itoa(port)) {
		t.Fatalf("npm didn't get the port:\n%s", log)
	}
}

func TestANodeSiteWhoseDevServerExitsSaysWhy(t *testing.T) {
	root, cfg := testRoot(t, func(c *config.Config) { nodeApp(t, c) })
	fakeNpm(t, root, "flock-test-exit")
	client := startDaemon(t, root)

	getPage(t, cfg, "app.test")
	deadline := time.Now().Add(20 * time.Second)
	for {
		s := siteStatus(t, client, "app")
		if s.NodeServer.State == api.NodeFailed {
			// No node_modules: that's what to fix first.
			if !strings.Contains(s.NodeServer.Problem, "npm install") {
				t.Fatalf("problem: %q", s.NodeServer.Problem)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not failed: %+v", s.NodeServer)
		}
		time.Sleep(100 * time.Millisecond)
	}

	code, page, body := getPage(t, cfg, "app.test")
	if code != http.StatusServiceUnavailable || page != "node-failed" || !strings.Contains(body, "Cannot find module") || !strings.Contains(body, `data-page="node-failed"`) {
		t.Fatalf("the page doesn't say why: %d %q\n%s", code, page, body)
	}
}

func TestRestartWakesANodeSite(t *testing.T) {
	root, _ := testRoot(t, func(c *config.Config) { nodeApp(t, c) })
	fakeNpm(t, root, "flock-test-server")
	client := startDaemon(t, root)

	if _, err := client.RestartSite(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for siteStatus(t, client, "app").NodeServer.State != api.NodeRunning {
		if time.Now().After(deadline) {
			t.Fatalf("not running: %+v\n%s", siteStatus(t, client, "app").NodeServer, readLog(t, root, "node-app"))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// quickSleep makes dev servers sleep after idle instead of 30 minutes, for the daemons started
// after it.
func quickSleep(t *testing.T, idle time.Duration) {
	t.Helper()
	oldIdle, oldCheck := nodeIdle, nodeSleepCheck
	nodeIdle, nodeSleepCheck = idle, 50*time.Millisecond
	t.Cleanup(func() { nodeIdle, nodeSleepCheck = oldIdle, oldCheck })
}

// waitNode waits until the site's dev server is in state.
func waitNode(t *testing.T, client *api.Client, root, state string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for siteStatus(t, client, "app").NodeServer.State != state {
		if time.Now().After(deadline) {
			t.Fatalf("not %s: %+v\n%s", state, siteStatus(t, client, "app").NodeServer, readLog(t, root, "node-app"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAnIdleNodeSiteGoesToSleepAndWakesAgain(t *testing.T) {
	quickSleep(t, 2*time.Second)
	root, cfg := testRoot(t, func(c *config.Config) { nodeApp(t, c) })
	fakeNpm(t, root, "flock-test-server")
	client := startDaemon(t, root)

	getPage(t, cfg, "app.test")
	waitNode(t, client, root, api.NodeRunning)
	port := siteStatus(t, client, "app").NodeServer.Port

	waitNode(t, client, root, api.NodeSleeping)
	if answers("http://localhost:" + strconv.Itoa(port)) {
		t.Fatal("the sleeping dev server still holds its port")
	}

	// The next request wakes it.
	if code, page, _ := getPage(t, cfg, "app.test"); page != "node-starting" {
		t.Fatalf("waking: %d %q", code, page)
	}
	waitNode(t, client, root, api.NodeRunning)
}

func TestAnOpenHotReloadConnectionKeepsANodeSiteAwake(t *testing.T) {
	quickSleep(t, 2*time.Second)
	root, cfg := testRoot(t, func(c *config.Config) { nodeApp(t, c) })
	fakeNpm(t, root, "flock-test-server")
	client := startDaemon(t, root)

	getPage(t, cfg, "app.test")
	waitNode(t, client, root, api.NodeRunning)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "GET /@vite/client HTTP/1.1\r\nHost: app.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if resp, err := http.ReadResponse(bufio.NewReader(conn), nil); err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %+v", err, resp)
	}

	// Several idle periods pass with the tab open.
	time.Sleep(5 * time.Second)
	if s := siteStatus(t, client, "app").NodeServer.State; s != api.NodeRunning {
		t.Fatalf("slept with a hot-reload connection open: %s", s)
	}

	conn.Close()
	waitNode(t, client, root, api.NodeSleeping)
}

func TestStoppingANodeSitePutsItToSleep(t *testing.T) {
	root, cfg := testRoot(t, func(c *config.Config) { nodeApp(t, c) })
	fakeNpm(t, root, "flock-test-server")
	client := startDaemon(t, root)

	getPage(t, cfg, "app.test")
	waitNode(t, client, root, api.NodeRunning)
	port := siteStatus(t, client, "app").NodeServer.Port

	if _, err := client.StopSite(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if s := siteStatus(t, client, "app").NodeServer.State; s != api.NodeSleeping || answers("http://localhost:"+strconv.Itoa(port)) {
		t.Fatalf("after the stop: %s", s)
	}
	if _, err := client.StopSite(context.Background(), "missing"); err == nil {
		t.Fatal("stopped a site that doesn't exist")
	}
}

func TestANodeSitesWorktreeRunsADevServerOfItsOwn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	parked := t.TempDir()
	repo := filepath.Join(parked, "app")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Flock", "-c", "user.email=flock@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeFile(t, filepath.Join(repo, "package.json"), `{"scripts": {"dev": "vite"}}`)
	git("add", ".")
	git("commit", "-q", "-m", "init")
	// Made before Flock looked for worktrees: served, not set up (no npm install).
	git("worktree", "add", "-q", "-b", "feature", filepath.Join(parked, "app-feature"))

	root, cfg := testRoot(t, func(c *config.Config) { c.Parked = []string{parked} })
	fakeNpm(t, root, "flock-test-server")
	client := startDaemon(t, root)

	wt := siteStatus(t, client, "feature.app")
	parent := siteStatus(t, client, "app")
	if wt.Type != config.TypeNode || wt.NodeServer == nil || wt.NodeServer.Port == parent.NodeServer.Port || wt.NodeServer.Log != "node-feature.app" {
		t.Fatalf("worktree %+v %+v, site %+v", wt, wt.NodeServer, parent.NodeServer)
	}

	getPage(t, cfg, "feature.app.test")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if code, _, body := getPage(t, cfg, "feature.app.test"); code == http.StatusOK {
			if body != "dev server for feature.app.test" {
				t.Fatalf("got %q", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worktree's dev server didn't answer:\n%s", readLog(t, root, "node-feature.app"))
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Only the worktree's dev server runs: the site's own sleeps until it's visited.
	if s := siteStatus(t, client, "app").NodeServer.State; s != api.NodeSleeping {
		t.Fatalf("the site's dev server: %s", s)
	}
}

func TestNextJSListensOnLoopbackAndANewNodeVersionRestarts(t *testing.T) {
	for script, next := range map[string]bool{
		"next dev": true, "next dev --turbopack": true, "prisma generate && next dev": true,
		"vite": false, "nuxt dev": false, "nextra dev": false, "astro dev": false,
	} {
		if nextDev.MatchString(script) != next {
			t.Errorf("%q: next dev %v", script, !next)
		}
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts": {"dev": "next dev --turbopack"}}`)
	d := &daemon{root: t.TempDir(), restarts: map[string]int{}}
	cfg := config.Default()
	site := config.Site{Name: "web", Path: dir, Type: config.TypeNode}
	spec := d.nodeSpec(cfg, site, 22001, "22", false)
	if !strings.HasSuffix(spec.Shell, "--port 22001 --hostname 127.0.0.1") {
		t.Errorf("next dev isn't bound to loopback: %q", spec.Shell)
	}
	if other := d.nodeSpec(cfg, site, 22001, "24", false); reflect.DeepEqual(spec.Env, other.Env) {
		t.Error("another Node.js version gives the same spec, so the server wouldn't restart")
	}
	// Only the site's own domain: Vite before 8 takes the variable as one name.
	if !slices.Contains(spec.Env, "__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS=.web.test") {
		t.Errorf("env: %v", spec.Env)
	}
}

func TestPreferredNodePortsAreStableAndInRange(t *testing.T) {
	for _, name := range []string{"app", "shop", "feature-x.shop"} {
		p := preferredNodePort(name)
		if p != preferredNodePort(name) || p < firstNodePort || p >= firstNodePort+nodePorts {
			t.Fatalf("%s: %d", name, p)
		}
	}
}

// viteScript is what Vite's /@vite/client answers: a script that logs "[vite]".
const viteScript = `console.debug("[vite] connecting...")`

// fakeVite serves Vite's client script at base + "@vite/client".
func fakeVite(t *testing.T, base string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != base+"@vite/client" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, viteScript)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPHPSiteDevServerRunningWithoutFlockIsFound(t *testing.T) {
	dir := t.TempDir()
	if externalDevServer(dir) != 0 {
		t.Fatal("no public/hot, yet a dev server of its own")
	}
	vite := fakeVite(t, "/")
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	// A server that answers 200 to every path isn't Vite either.
	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer always.Close()
	u, _ := url.Parse(vite.URL)
	vitePort, _ := strconv.Atoi(u.Port())
	if err := os.Mkdir(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	hot := filepath.Join(dir, "public", "hot")

	writeFile(t, hot, vite.URL+"\n")
	if got := externalDevServer(dir); got != vitePort {
		t.Fatalf("a Vite in public/hot: port %d, want %d", got, vitePort)
	}
	// A stale public/hot, with another program on its port, isn't a Vite.
	for _, stale := range []*httptest.Server{other, always} {
		writeFile(t, hot, stale.URL+"\n")
		if externalDevServer(dir) != 0 {
			t.Fatalf("%s taken for a Vite", stale.URL)
		}
	}

	// vite-plugin-symfony says where Vite listens in its build directory's entrypoints.json, with its base.
	if err := os.Remove(hot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "public", "build", ".vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	entrypoints := filepath.Join(dir, "public", "build", "entrypoints.json")
	writeFile(t, entrypoints, `{"base":"/","isProd":false,"viteServer":{"origin":"`+vite.URL+`"},"entryPoints":{}}`)
	if got := externalDevServer(dir); got != vitePort {
		t.Fatalf("a Vite in entrypoints.json: port %d, want %d", got, vitePort)
	}
	based := fakeVite(t, "/build/")
	u, _ = url.Parse(based.URL)
	basedPort, _ := strconv.Atoi(u.Port())
	writeFile(t, entrypoints, `{"base":"/build/","viteServer":{"origin":"`+based.URL+`"}}`)
	if got := externalDevServer(dir); got != basedPort {
		t.Fatalf("a Vite under /build/: port %d, want %d", got, basedPort)
	}
	// The plugin's newer versions write viteServer as a URL string, with base at the top.
	writeFile(t, entrypoints, `{"base":"/build/","isProd":false,"viteServer":"`+based.URL+`","entryPoints":{}}`)
	if got := externalDevServer(dir); got != basedPort {
		t.Fatalf("a Vite as a viteServer URL string: port %d, want %d", got, basedPort)
	}
	// Built files (viteServer is false), or the origin of another program, aren't a dev server.
	writeFile(t, entrypoints, `{"isProd":true,"viteServer":false,"entryPoints":{}}`)
	if externalDevServer(dir) != 0 {
		t.Fatal("built files taken for a dev server")
	}
	writeFile(t, entrypoints, `{"viteServer":{"origin":"`+other.URL+`"}}`)
	if externalDevServer(dir) != 0 {
		t.Fatal("a server that isn't Vite taken for one")
	}
}

func TestAVitePublicHotIsOutsideFlockUnlessItsProcessIsFlocks(t *testing.T) {
	if runningAsRoot() {
		t.Skip("Flock doesn't run a project's dev server as root")
	}
	root := shortTempDir(t)
	fakeNpm(t, root, "flock-test-server")
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	vite := fakeVite(t, "/")
	if err := os.Mkdir(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "public", "hot"), vite.URL+"\n")
	d := &daemon{root: root, procs: supervisor.New(), restarts: map[string]int{}}
	t.Cleanup(func() { d.procs.Retain(map[string]bool{}) })

	// Nothing of Flock's runs: the Vite in public/hot is the project's own.
	u, _ := url.Parse(vite.URL)
	if got := strconv.Itoa(d.outsideDevServer("app", dir)); got != u.Port() {
		t.Fatalf("outside port %s, want %s", got, u.Port())
	}
	// Flock's own process runs: the Vite in public/hot is its own, even on the same port.
	spec := d.nodeSpec(config.Default(), config.Site{Name: "app", Path: dir}, freePort(t), "22", false)
	d.procs.Ensure(spec)
	deadline := time.Now().Add(5 * time.Second)
	for !d.procUp("node-app") {
		if time.Now().After(deadline) {
			t.Fatal("the fake dev server didn't start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := d.outsideDevServer("app", dir); got != 0 {
		t.Fatalf("Flock's own process taken for an outside Vite on port %d", got)
	}
}

func TestOnlyAPlainViteDevScriptGetsADevServerBesidePHP(t *testing.T) {
	for script, want := range map[string]bool{
		"vite": true, "vite --host": true, "npx vite": true, "vite --port=3000": true,
		"": false, "vite build": false, "mix": false, "webpack --watch": false,
		"concurrently \"php artisan serve\" vite": false, "npm run vite": false, "vite && php artisan queue:work": false,
	} {
		if got := isViteDev(script); got != want {
			t.Errorf("%q: got %v, want %v", script, got, want)
		}
	}
}

func TestAViteConfigsFixedPortIsRespected(t *testing.T) {
	dir := t.TempDir()
	if got := viteConfigPort(dir); got != 0 {
		t.Fatalf("no config, port %d", got)
	}
	for config, want := range map[string]int{
		"export default { server: { host: true, port: 5199 } }":                      5199,
		"export default { server: { hmr: { port: 24678 }, port: 5200 } }":            5200,
		"export default { server: { hmr: { port: 24678 } } }":                        0,
		"export default { preview: { port: 4173 } }":                                 0,
		"export default { server: { host: true } } // port: 3000":                    0,
		"export default { server: {\n // port: 3000\n port: 5201 /* port: 1 */ } }":  5201,
		`export default { server: { origin: "http://localhost:5199", port: 5199 } }`: 5199,
		`export default { server: { proxy: { "/api": "http://x" }, port: 5202 } }`:   5202,
	} {
		writeFile(t, filepath.Join(dir, "vite.config.ts"), config)
		if got := viteConfigPort(dir); got != want {
			t.Errorf("%q: got port %d, want %d", config, got, want)
		}
	}

	writeFile(t, filepath.Join(dir, "vite.config.ts"), "export default { server: { host: true, port: 5199 } }")
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts": {"dev": "vite"}}`)
	d := &daemon{root: t.TempDir(), restarts: map[string]int{}, procs: supervisor.New()}
	site := config.Site{Name: "web", Path: dir}
	taken := map[int]bool{}
	spec, port, ok := d.phpDevServer(config.Default(), site, "22", taken)
	if !ok || port != 5199 || spec.Shell != "npm run dev" || !taken[5199] {
		t.Fatalf("ok %v, port %d, command %q: Flock passed its own port", ok, port, spec.Shell)
	}
	// A second project fixing the same port gets a port of Flock's.
	spec, port, ok = d.phpDevServer(config.Default(), config.Site{Name: "other", Path: dir}, "22", taken)
	if !ok || port == 5199 || spec.Shell != "npm run dev -- --port "+strconv.Itoa(port) {
		t.Fatalf("a taken port: ok %v, port %d, command %q", ok, port, spec.Shell)
	}

	// A fixed port another program listens on isn't taken: Vite would move to the next one.
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	busyPort := busy.Addr().(*net.TCPAddr).Port
	writeFile(t, filepath.Join(dir, "vite.config.ts"), "export default { server: { port: "+strconv.Itoa(busyPort)+" } }")
	spec, port, ok = d.phpDevServer(config.Default(), site, "22", map[int]bool{})
	if !ok || port == busyPort || spec.Shell != "npm run dev -- --port "+strconv.Itoa(port) {
		t.Fatalf("a busy fixed port: ok %v, port %d, command %q", ok, port, spec.Shell)
	}

	if err := os.Remove(filepath.Join(dir, "vite.config.ts")); err != nil {
		t.Fatal(err)
	}
	spec, port, ok = d.phpDevServer(config.Default(), site, "22", map[int]bool{})
	if !ok || spec.Shell != "npm run dev -- --port "+strconv.Itoa(port) {
		t.Fatalf("ok %v, port %d, command %q", ok, port, spec.Shell)
	}
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts": {"dev": "mix"}}`)
	if _, _, ok := d.phpDevServer(config.Default(), site, "22", map[int]bool{}); ok {
		t.Fatal("a dev script that isn't Vite got a dev server")
	}
}

func TestARequestWaitsForTheDevServerOfAPHPSiteToAnswer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	d := &daemon{procs: supervisor.New()}
	d.node.ports = map[string]int{"app": port}
	d.node.specs = map[string]supervisor.Spec{"app": {Dir: t.TempDir()}}
	d.node.woke = map[string]time.Time{"app": time.Now()}
	start := time.Now()
	d.awaitDevServer("app")
	if time.Since(start) > time.Second {
		t.Fatal("waited for a server that answers")
	}

	// A server that doesn't answer is waited for no longer than devWait after it was woken.
	srv.Close()
	old := devWait
	devWait = 200 * time.Millisecond
	defer func() { devWait = old }()
	d.node.woke["app"] = time.Now().Add(-time.Second)
	start = time.Now()
	d.awaitDevServer("app")
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("waited past the window")
	}
}

func TestAPHPSitesViteStartsOnTheFirstRequestAndSleepsOnceNoTabIsOpen(t *testing.T) {
	if runningAsRoot() {
		t.Skip("Flock doesn't run a project's dev server as root")
	}
	root := shortTempDir(t)
	fakeNpm(t, root, "flock-test-server")
	if err := os.MkdirAll(filepath.Join(root, "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"scripts": {"dev": "vite"}}`)
	d := &daemon{root: root, procs: supervisor.New(), restarts: map[string]int{}}
	t.Cleanup(func() { d.procs.Retain(map[string]bool{}) })

	spec, port, ok := d.phpDevServer(config.Default(), config.Site{Name: "app", Path: dir}, "22", map[int]bool{})
	if !ok {
		t.Fatal("no dev server for a Vite project")
	}
	d.node.keep(map[string]supervisor.Spec{"app": spec}, map[string]int{"app": port}, map[string]bool{"app": true})

	// The first request starts Vite and waits for it: PHP renders @vite once public/hot exists.
	_, _, done := d.wakeNode("app")
	if done == nil {
		t.Fatal("the request wasn't taken for a site with a dev server")
	}
	if !answers("http://localhost:" + strconv.Itoa(port)) {
		t.Fatalf("the request returned before the dev server answered:\n%s", readLog(t, root, "node-app"))
	}
	done()

	// A dev server replaced by a new spec (a restart) makes requests wait for it again.
	before := d.node.woke["app"]
	time.Sleep(20 * time.Millisecond)
	d.restarts["app"] = 1
	d.ensureIfAwake("app", d.nodeSpec(config.Default(), config.Site{Name: "app", Path: dir}, port, "22", false), true, map[string]bool{})
	if !d.node.woke["app"].After(before) {
		t.Fatal("the wait for the dev server didn't start again after a restart")
	}
	d.awaitDevServer("app")
	if !answers("http://localhost:" + strconv.Itoa(port)) {
		t.Fatal("the wait ended before the restarted dev server answered")
	}

	// An open tab (a connection straight to Vite) keeps it awake past the idle time.
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.sleepNodeSites(ctx, 50*time.Millisecond, 20*time.Millisecond)
	time.Sleep(500 * time.Millisecond)
	if !d.node.isAwake("app") {
		t.Fatal("slept with a browser connected to it")
	}

	// Once it's closed, the server sleeps.
	conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for d.node.isAwake("app") {
		if time.Now().After(deadline) {
			t.Fatal("still awake without a connection")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAViteAdvertisedOverHTTPSIsProbedOverHTTPS(t *testing.T) {
	dir := t.TempDir()
	vite := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@vite/client" {
			_, _ = io.WriteString(w, "console.debug('[vite] connecting...')")
			return
		}
		http.NotFound(w, r)
	}))
	defer vite.Close()
	if err := os.Mkdir(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "public", "hot"), vite.URL+"\n")
	origins := devServerOrigins(dir)
	if len(origins) != 1 || origins[0].scheme != "https" || origins[0].host != "127.0.0.1" {
		t.Fatalf("origins = %+v, want one over https on the advertised host", origins)
	}
	// The test server's certificate isn't trusted, so the check, which validates it, finds no Vite;
	// what matters is that it speaks TLS to the listener rather than plain HTTP.
	if isVite(devOrigin{scheme: "http", host: origins[0].host, port: origins[0].port, base: "/"}) {
		t.Fatal("plain HTTP to a TLS listener taken for a Vite")
	}
}
