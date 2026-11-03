package daemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
)

// fakeNode is what this test binary does when it runs as node: like `npm run dev`, it serves
// $PORT until it's stopped, answering with the Host, its arguments and the hosts Vite is told about.
func fakeNode() int {
	ln, err := net.Listen("tcp", "127.0.0.1:"+os.Getenv("PORT"))
	if err != nil {
		return 1
	}
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "node "+r.Host+" "+strings.Join(os.Args[1:], " ")+" vite:"+os.Getenv("__VITE_ADDITIONAL_SERVER_ALLOWED_HOSTS"))
		}))
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	<-stop
	return 0
}

// installFakeNode puts this test binary into root as Node.js 22.1.0, with a bundled npm.
func installFakeNode(t *testing.T, root string) {
	t.Helper()
	version := filepath.Join(root, "node", "22.1.0")
	bin, modules, exe := filepath.Join(version, "bin"), filepath.Join(version, "lib", "node_modules"), "node"
	if runtime.GOOS == "windows" {
		bin, modules, exe = version, filepath.Join(version, "node_modules"), "node.exe"
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{bin, filepath.Join(modules, "npm", "bin")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, exe), data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, "npm", "package.json"), []byte(`{"version":"10.9.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func nodeProject(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{`+script+`}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
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
	t.Fatalf("no site %s in %+v", name, st.Sites)
	return api.SiteStatus{}
}

func TestNodeSitesRunTheirDevServer(t *testing.T) {
	project := nodeProject(t, `"dev":"next dev"`)
	port := freePort(t)
	root, cfg := testRoot(t, func(c *config.Config) {
		c.Sites = []config.Site{{Name: "app", Path: project, Server: config.ServerNode, Port: port, Domains: []string{"app-2.test"}}}
	})
	installFakeNode(t, root)
	client := startDaemon(t, root)

	deadline := time.Now().Add(10 * time.Second)
	for siteStatus(t, client, "app").NodeServer.State != api.NodeRunning {
		if time.Now().After(deadline) {
			t.Fatalf("not running: %+v", siteStatus(t, client, "app").NodeServer)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ns := siteStatus(t, client, "app").NodeServer; ns.Version != "22.1.0" || ns.Port != port || ns.Log != "node-app" {
		t.Fatalf("unexpected dev server: %+v", ns)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "app.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"node app.test ", " run dev -- --port " + strconv.Itoa(port) + " --hostname 127.0.0.1 ", "vite:app.test,app-2.test"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%q lacks %q", body, want)
		}
	}

	// Back on PHP, the dev server stops and frees its port.
	cfg.Sites[0].Server = ""
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for !portFree(port) {
		if time.Now().After(deadline) {
			t.Fatal("the dev server still holds its port")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestNodeSitesSayWhyTheyCantRun(t *testing.T) {
	noScript := nodeProject(t, `"build":"next build"`)
	pinned := nodeProject(t, `"dev":"vite"`)
	if err := os.WriteFile(filepath.Join(pinned, ".nvmrc"), []byte("18\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, _ := testRoot(t, func(c *config.Config) {
		c.Sites = []config.Site{
			{Name: "noscript", Path: noScript, Server: config.ServerNode, Port: freePort(t)},
			{Name: "pinned", Path: pinned, Server: config.ServerNode, Port: freePort(t)},
		}
	})
	installFakeNode(t, root)
	client := startDaemon(t, root)

	if ns := siteStatus(t, client, "noscript").NodeServer; ns.State != api.NodeFailed || !strings.Contains(ns.Problem, `no "dev" script`) {
		t.Errorf("noscript: %+v", ns)
	}
	if ns := siteStatus(t, client, "pinned").NodeServer; ns.State != api.NodeUnavailable || !strings.Contains(ns.Problem, "flock node install 18") {
		t.Errorf("pinned: %+v", ns)
	}
}

func portFree(port int) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func TestTheWorktreesOfANodeSiteRunDevServersOfTheirOwn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	project := nodeProject(t, `"dev":"vite"`)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", project, "-c", "user.name=Flock", "-c", "user.email=flock@example.test"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	git("worktree", "add", "-q", "-b", "feature", filepath.Join(t.TempDir(), "feature"))

	root, cfg := testRoot(t, func(c *config.Config) {
		c.Sites = []config.Site{{Name: "app", Path: project, Server: config.ServerNode, Port: freePort(t)}}
	})
	installFakeNode(t, root)
	client := startDaemon(t, root)

	deadline := time.Now().Add(10 * time.Second)
	for {
		wt := siteStatus(t, client, "feature.app")
		if wt.Server == config.ServerNode && wt.NodeServer != nil && wt.NodeServer.State == api.NodeRunning {
			if wt.NodeServer.Port == siteStatus(t, client, "app").NodeServer.Port || wt.NodeServer.Port < config.FirstWorktreePort {
				t.Fatalf("the worktree's port: %+v", wt.NodeServer)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worktree's dev server: %+v %+v", wt.Server, wt.NodeServer)
		}
		time.Sleep(50 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "feature.app.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), "node feature.app.test ") {
		t.Fatalf("got %q", body)
	}
}

func TestANodeSiteRunsACommandOfItsOwn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command runs through /bin/sh here")
	}
	project := t.TempDir() // no package.json: a command of its own needs no dev script
	port := freePort(t)
	root, cfg := testRoot(t, func(c *config.Config) {
		c.Sites = []config.Site{{Name: "app", Path: project, Server: config.ServerNode, Port: port, Command: "node serve --port {port}"}}
	})
	installFakeNode(t, root)
	client := startDaemon(t, root)

	deadline := time.Now().Add(10 * time.Second)
	for siteStatus(t, client, "app").NodeServer.State != api.NodeRunning {
		if time.Now().After(deadline) {
			t.Fatalf("not running: %+v", siteStatus(t, client, "app").NodeServer)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ns := siteStatus(t, client, "app").NodeServer; !ns.Custom || ns.Command != "node serve --port "+strconv.Itoa(port) || ns.Script != "" {
		t.Fatalf("unexpected dev server: %+v", ns)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "app.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := "node app.test serve --port " + strconv.Itoa(port) + " "; !strings.HasPrefix(string(body), want) {
		t.Fatalf("got %q, want %q…", body, want)
	}
}
