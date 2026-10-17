package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"flock/internal/api"
	"flock/internal/catalog"
	"flock/internal/components"
	"flock/internal/php"
)

// fakeDaemon answers like the daemon and records the changes asked of it.
type fakeDaemon struct {
	mu       sync.Mutex
	down     bool
	status   api.Status
	cfg      api.ConfigView
	services []api.Service
	catalog  api.Catalog
	php      api.PHPVersions
	// phpPolls is how many PHPVersions calls a PHP install takes to finish (0: never).
	phpPolls int
	polled   int
	changes  []string
}

func (f *fakeDaemon) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := json.Marshal(args)
	f.changes = append(f.changes, format+" "+string(b))
}

func (f *fakeDaemon) err() error {
	if f.down {
		return api.ErrDaemonNotRunning
	}
	return nil
}

func (f *fakeDaemon) Status(context.Context) (api.Status, error)      { return f.status, f.err() }
func (f *fakeDaemon) Config(context.Context) (api.ConfigView, error)  { return f.cfg, f.err() }
func (f *fakeDaemon) Services(context.Context) ([]api.Service, error) { return f.services, f.err() }
func (f *fakeDaemon) Catalog(context.Context) (api.Catalog, error)    { return f.catalog, f.err() }
func (f *fakeDaemon) Components(context.Context) (api.Components, error) {
	return api.Components{}, f.err()
}
func (f *fakeDaemon) PostgresVersions(context.Context) (api.PostgresVersions, error) {
	return api.PostgresVersions{}, f.err()
}
func (f *fakeDaemon) CheckPHP(ctx context.Context) (api.PHPVersions, error) {
	return f.PHPVersions(ctx)
}
func (f *fakeDaemon) NodeVersions(context.Context) (api.NodeVersions, error) {
	return api.NodeVersions{}, f.err()
}

func (f *fakeDaemon) PHPVersions(context.Context) (api.PHPVersions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polled++
	for i, v := range f.php.Versions {
		if v.Job != nil && f.phpPolls > 0 && f.polled >= f.phpPolls {
			f.php.Versions[i].Job, f.php.Versions[i].Installed = nil, v.Latest
		}
	}
	return f.php, f.err()
}

func (f *fakeDaemon) InstallPHP(_ context.Context, minor string) (api.PHPVersions, error) {
	f.record("install php", minor)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polled = 0
	for i, v := range f.php.Versions {
		if v.Minor == minor {
			f.php.Versions[i].Job = &php.Job{State: "running", Phase: "downloading", Progress: 0.5}
		}
	}
	return f.php, nil
}

func (f *fakeDaemon) SetServiceRunning(_ context.Context, id string, on bool) ([]api.Service, error) {
	f.record("service", id, on)
	return f.services, nil
}

func (f *fakeDaemon) InstallService(_ context.Context, id, line string, port int) (api.Catalog, error) {
	f.record("install service", id, line, port)
	return f.catalog, nil
}

func (f *fakeDaemon) InstallComponent(_ context.Context, id string) (api.Components, error) {
	f.record("install component", id)
	return api.Components{}, nil
}

func (f *fakeDaemon) InstallPostgres(_ context.Context, major, version string) (api.PostgresVersions, error) {
	f.record("install postgres", major, version)
	return api.PostgresVersions{}, nil
}

func (f *fakeDaemon) InstallNode(_ context.Context, major string) (api.NodeVersions, error) {
	f.record("install node", major)
	return api.NodeVersions{}, nil
}

func (f *fakeDaemon) LinkSite(_ context.Context, dir, name string) (api.Status, error) {
	f.record("link", dir, name)
	st := f.status
	st.Sites = append(st.Sites, api.SiteStatus{Name: filepath.Base(dir), Domain: filepath.Base(dir) + ".test", Path: dir, URL: "https://" + filepath.Base(dir) + ".test"})
	return st, nil
}

func (f *fakeDaemon) UpdateSite(_ context.Context, name string, c api.SiteChange) (api.Status, error) {
	f.record("update", name, c)
	return f.status, nil
}

func (f *fakeDaemon) AddDomain(_ context.Context, name, domain string) (api.Status, error) {
	f.record("domain", name, domain)
	return f.status, nil
}

func (f *fakeDaemon) PinSite(_ context.Context, name, tool, version string) (api.Status, error) {
	f.record("pin", name, tool, version)
	return f.status, nil
}

func (f *fakeDaemon) UnpinSite(_ context.Context, name, tool string) (api.Status, error) {
	f.record("unpin", name, tool)
	return f.status, nil
}

func (f *fakeDaemon) RestartSite(_ context.Context, name string) (api.Status, error) {
	f.record("restart", name)
	return f.status, nil
}

func (f *fakeDaemon) UpdateSettings(_ context.Context, s api.Settings) (api.Status, error) {
	f.record("settings", s)
	return f.status, nil
}

// fixture is a Flock root with one site, "shop", in a project folder.
func fixture(t *testing.T) (*fakeDaemon, Options) {
	t.Helper()
	root, project := t.TempDir(), t.TempDir()
	d := &fakeDaemon{
		status: api.Status{Dumps: true, Sites: []api.SiteStatus{{
			Name: "shop", Domain: "shop.test", Path: project, URL: "https://shop.test", Secured: true,
			PHP: "8.4", PHPSource: "project", PHPFile: filepath.Join(project, ".flockrc"), Node: "22", NodeSource: "default",
			Server: "fastcgi", Domains: []api.SiteDomain{{Entry: "tenant1", Host: "tenant1.shop.test", URL: "https://tenant1.shop.test"}},
		}}},
		cfg: api.ConfigView{DefaultPHP: "8.5", DefaultNode: "22"},
	}
	return d, Options{Version: "test", Root: root, Daemon: d, Dir: filepath.Join(project, "app", "Http"), Wait: 2 * time.Second}
}

func connect(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(ctx, opts).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls a tool and decodes its structured result into out; it returns the text of an error result.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return res.Content[0].(*mcp.TextContent).Text
	}
	if out != nil {
		data, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: %v in %s", name, err, data)
		}
	}
	return ""
}

func TestToolsAndAnnotations(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{}
	for _, tool := range res.Tools {
		readOnly[tool.Name] = tool.Annotations != nil && tool.Annotations.ReadOnlyHint
		if tool.Annotations != nil && !tool.Annotations.ReadOnlyHint && (tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint) {
			t.Errorf("%s may be destructive; Flock's tools never remove anything", tool.Name)
		}
	}
	for _, name := range []string{"status", "site_info", "list_sites", "list_services", "list_php_versions", "list_node_versions",
		"get_errors", "get_dumps", "list_mail", "read_mail", "service_logs"} {
		if !readOnly[name] {
			t.Errorf("%s should be a read-only tool", name)
		}
	}
	for _, name := range []string{"service", "install_version", "link_site", "update_site", "set_debugging", "create_database"} {
		if ro, ok := readOnly[name]; !ok || ro {
			t.Errorf("%s should be a tool that changes things (listed: %v)", name, ok)
		}
	}
	if len(res.Tools) != 17 {
		t.Errorf("got %d tools", len(res.Tools))
	}
}

func TestInstructionsDescribeTheProject(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)

	got := cs.InitializeResult().Instructions
	for _, want := range []string{`"shop"`, "https://shop.test", "PHP 8.4 (pinned in .flockrc)", "Node.js 22 (Flock's default)", "php artisan serve"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "herd") {
		t.Error("the instructions must not name other tools")
	}

	d, opts := fixture(t)
	d.down = true
	if got := connect(t, opts).InitializeResult().Instructions; !strings.Contains(got, "isn't running") {
		t.Errorf("daemon down: %s", got)
	}
}

func TestSiteResolution(t *testing.T) {
	d, opts := fixture(t)
	project := d.status.Sites[0].Path
	cs := connect(t, opts)

	var info siteInfo
	if e := callTool(t, cs, "site_info", nil, &info); e != "" || info.Name != "shop" {
		t.Fatalf("from the working directory: %q %+v", e, info)
	}
	if info.PHP.Source != "project" || len(info.OtherURLs) != 1 || !info.HTTPS {
		t.Fatalf("details: %+v", info)
	}
	for _, ref := range []string{"shop", "shop.test", "https://tenant1.shop.test/", filepath.Join(project, "routes")} {
		if e := callTool(t, cs, "site_info", map[string]any{"site": ref}, &info); e != "" || info.Name != "shop" {
			t.Errorf("site %q: %q", ref, e)
		}
	}
	if e := callTool(t, cs, "site_info", map[string]any{"site": "nope"}, nil); !strings.Contains(e, "no Flock site") {
		t.Errorf("unknown site: %q", e)
	}

	opts.Dir = t.TempDir()
	if e := callTool(t, connect(t, opts), "site_info", nil, nil); !strings.Contains(e, "pass `site`") {
		t.Errorf("outside every site: %q", e)
	}
	opts.Site = "shop"
	if e := callTool(t, connect(t, opts), "site_info", nil, &info); e != "" || info.Name != "shop" {
		t.Errorf("FLOCK_SITE: %q", e)
	}
}

func TestGetErrorsReadsTheLaravelLogAndPHPLog(t *testing.T) {
	d, opts := fixture(t)
	project := d.status.Sites[0].Path
	logs := filepath.Join(project, "storage", "logs")
	_ = os.MkdirAll(logs, 0o755)
	_ = os.WriteFile(filepath.Join(logs, "laravel.log"), []byte(
		"[2026-10-01 10:00:00] local.INFO: started\n"+
			"[2026-10-01 10:00:01] local.ERROR: Undefined variable $cart {\"exception\":\"x\"}\n#0 trace line\n"+
			"[2026-10-01 10:00:02] local.DEBUG: noise\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(opts.Root, "log"), 0o755)
	phpLog := filepath.Join(opts.Root, "log", php.PoolLog("8.4")+".log")
	_ = os.WriteFile(phpLog, []byte("NOTICE: ready\nWARNING: child said into stderr: \"PHP Parse error: syntax error in "+
		filepath.Join(project, "routes", "web.php")+" on line 3\"\nPHP Fatal error: elsewhere in /other/app.php\n"), 0o644)
	cs := connect(t, opts)

	var out errorsOut
	if e := callTool(t, cs, "get_errors", nil, &out); e != "" {
		t.Fatal(e)
	}
	if len(out.Entries) != 1 || !strings.EqualFold(out.Entries[0].Level, "error") || !strings.Contains(out.Entries[0].Details, "trace line") {
		t.Fatalf("errors only: %+v", out.Entries)
	}
	if len(out.PHPErrors) != 1 || !strings.Contains(out.PHPErrors[0], "Parse error") {
		t.Fatalf("PHP errors of this site only: %q", out.PHPErrors)
	}
	if callTool(t, cs, "get_errors", map[string]any{"all_levels": true}, &out); len(out.Entries) != 3 || !strings.EqualFold(out.Entries[0].Level, "debug") {
		t.Fatalf("all levels, newest first: %+v", out.Entries)
	}
}

func TestDumpsAreFilteredBySite(t *testing.T) {
	d, opts := fixture(t)
	project := d.status.Sites[0].Path
	_ = os.MkdirAll(filepath.Join(opts.Root, "dumps"), 0o755)
	lines := []string{
		`{"id":"1","text":"\"shop dump\"","source":{"file":` + jsonString(filepath.Join(project, "app", "x.php")) + `,"line":7}}`,
		`{"id":"2","text":"\"other\"","request":{"method":"GET","uri":"https://blog.test/"}}`,
		`{"id":"3","text":"\"by host\"","request":{"method":"POST","uri":"https://tenant1.shop.test/cart"}}`,
	}
	_ = os.WriteFile(filepath.Join(opts.Root, "dumps", "dumps.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	cs := connect(t, opts)

	var out dumpsOut
	callTool(t, cs, "get_dumps", nil, &out)
	if len(out.Dumps) != 3 || out.Dumps[0].Value != `"by host"` {
		t.Fatalf("every site, newest first: %+v", out.Dumps)
	}
	callTool(t, cs, "get_dumps", map[string]any{"site": "shop"}, &out)
	if len(out.Dumps) != 2 || out.Dumps[1].Source != filepath.Join(project, "app", "x.php")+":7" || out.Dumps[0].Request != "POST https://tenant1.shop.test/cart" {
		t.Fatalf("shop only: %+v", out.Dumps)
	}

	d.status.Dumps = false
	callTool(t, cs, "get_dumps", nil, &out)
	if out.Capturing || !strings.Contains(out.Note, "set_debugging") {
		t.Fatalf("capture off: %+v", out)
	}
}

func TestMailTools(t *testing.T) {
	_, opts := fixture(t)
	path := filepath.Join(opts.Root, "mail", "shop", "1-000001.eml")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw := "From: Shop <hi@shop.test>\r\nTo: ann@example.com\r\nSubject: Verify your email\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html\r\n\r\n<html><head><style>p{}</style></head><body><p>Hi Ann,</p>" +
		"<p><a href=\"https://shop.test/verify?id=1&amp;sig=abc\">Verify</a></p></body></html>\r\n"
	_ = os.WriteFile(path, []byte(raw), 0o600)
	cs := connect(t, opts)

	var list listMailOut
	callTool(t, cs, "list_mail", map[string]any{"search": "verify"}, &list)
	if len(list.Messages) != 1 || list.Messages[0].ID != "1-000001" {
		t.Fatalf("list: %+v", list)
	}
	if callTool(t, cs, "list_mail", map[string]any{"search": "invoice"}, &list); len(list.Messages) != 0 {
		t.Fatalf("search: %+v", list)
	}

	var msg readMailOut
	callTool(t, cs, "read_mail", map[string]any{"id": "1-000001"}, &msg)
	if len(msg.Links) != 1 || msg.Links[0] != "https://shop.test/verify?id=1&sig=abc" {
		t.Fatalf("links: %q", msg.Links)
	}
	if !strings.Contains(msg.Text, "Hi Ann,") || strings.Contains(msg.Text, "<p>") || strings.Contains(msg.Text, "p{}") {
		t.Fatalf("text: %q", msg.Text)
	}
	if e := callTool(t, cs, "read_mail", map[string]any{"id": "nope"}, nil); !strings.Contains(e, "list_mail") {
		t.Fatalf("unknown id: %q", e)
	}
}

func TestInstallPHPWaitsAndReportsProgress(t *testing.T) {
	d, opts := fixture(t)
	d.php = api.PHPVersions{Versions: []api.PHPRelease{{Release: php.Release{Minor: "8.3", Latest: "8.3.20", Status: "available"}}}}
	d.phpPolls = 3
	cs := connect(t, opts)

	var out actionOut
	if e := callTool(t, cs, "install_version", map[string]any{"tool": "php", "version": "8.3"}, &out); e != "" || !out.Done || !strings.Contains(out.Message, "8.3.20") {
		t.Fatalf("%q %+v", e, out)
	}

	// An install that outlasts the wait answers that it's still running.
	d.php.Versions[0].Installed = ""
	d.phpPolls = 0
	opts.Wait = 300 * time.Millisecond
	if e := callTool(t, connect(t, opts), "install_version", map[string]any{"tool": "php", "version": "8.3"}, &out); e != "" || out.Done || !strings.Contains(out.Message, "list_php_versions") {
		t.Fatalf("%q %+v", e, out)
	}

	if e := callTool(t, cs, "install_version", map[string]any{"tool": "ruby", "version": "3"}, nil); e == "" {
		t.Fatal("an unknown tool must be rejected by the schema")
	}
}

func TestInstallCatalogService(t *testing.T) {
	d, opts := fixture(t)
	d.catalog = api.Catalog{Services: []catalog.Entry{{ID: "mysql", Name: "MySQL"}}}
	d.services = []api.Service{{ID: "mysql", Name: "MySQL", Version: "8.4.11", Port: 3306, Installed: true, Enabled: true, Running: true, Env: "DB_CONNECTION=mysql"}}
	cs := connect(t, opts)

	var out actionOut
	if e := callTool(t, cs, "service", map[string]any{"action": "install", "service": "mysql", "version": "8.4"}, &out); e != "" || !out.Done {
		t.Fatalf("%q %+v", e, out)
	}
	if !strings.Contains(out.Message, "running on 127.0.0.1:3306") || !strings.Contains(out.Message, "DB_CONNECTION=mysql") {
		t.Fatalf("message: %s", out.Message)
	}

	d.catalog.Services[0].Job = &catalog.Job{Job: components.Job{State: "failed", Error: "checksum mismatch"}}
	if e := callTool(t, cs, "service", map[string]any{"action": "install", "service": "mysql"}, nil); !strings.Contains(e, "checksum mismatch") {
		t.Fatalf("failed install: %q", e)
	}
}

func TestUpdateSiteActions(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "isolate", "tool": "php", "version": "8.3"}, `update ["shop",{"php":"8.3"}]`},
		{map[string]any{"action": "unisolate", "tool": "node"}, `update ["shop",{"node":""}]`},
		{map[string]any{"action": "pin", "tool": "php"}, `pin ["shop","php",""]`},
		{map[string]any{"action": "unsecure"}, `update ["shop",{"secured":false}]`},
		{map[string]any{"action": "set_server", "server": "frankenphp", "worker": "octane"}, `update ["shop",{"server":"frankenphp","worker":"octane"}]`},
		{map[string]any{"action": "add_domain", "domain": "tenant2"}, `domain ["shop","tenant2"]`},
		{map[string]any{"action": "restart", "site": "shop.test"}, `restart ["shop"]`},
	} {
		d.changes = nil
		if e := callTool(t, cs, "update_site", c.args, nil); e != "" {
			t.Errorf("%v: %s", c.args, e)
			continue
		}
		if len(d.changes) != 1 || d.changes[0] != c.want {
			t.Errorf("%v: got %q, want %q", c.args, d.changes, c.want)
		}
	}
	if e := callTool(t, cs, "update_site", map[string]any{"action": "isolate", "tool": "php"}, nil); !strings.Contains(e, "needs version") {
		t.Errorf("isolate without a version: %q", e)
	}
}

func TestLinkSiteAndDebugging(t *testing.T) {
	d, opts := fixture(t)
	opts.Dir = filepath.Join(t.TempDir(), "blog")
	_ = os.Mkdir(opts.Dir, 0o755)
	cs := connect(t, opts)

	var out siteActionOut
	if e := callTool(t, cs, "link_site", nil, &out); e != "" || out.Site == nil || out.Site.URL != "https://blog.test" {
		t.Fatalf("%q %+v", e, out)
	}
	if d.changes[0] != `link [`+jsonString(opts.Dir)+`,""]` {
		t.Fatalf("changes: %q", d.changes)
	}

	d.changes = nil
	callTool(t, cs, "set_debugging", map[string]any{"feature": "dumps", "enabled": true}, nil)
	if len(d.changes) != 1 || !strings.Contains(d.changes[0], `"dumps":true`) || !strings.Contains(d.changes[0], `"xdebug":null`) {
		t.Fatalf("settings: %q", d.changes)
	}
}

func TestCreateDatabaseNeedsARunningService(t *testing.T) {
	d, opts := fixture(t)
	cs := connect(t, opts)
	if e := callTool(t, cs, "create_database", map[string]any{"name": "shop"}, nil); !strings.Contains(e, "no database service runs") {
		t.Fatalf("nothing running: %q", e)
	}
	d.services = []api.Service{{ID: "mysql", Name: "MySQL", Installed: true}, {ID: "postgres", Name: "PostgreSQL", Installed: true, Running: true}}
	if e := callTool(t, cs, "create_database", map[string]any{"name": "shop", "service": "mysql"}, nil); !strings.Contains(e, "isn't running") {
		t.Fatalf("stopped service: %q", e)
	}
}

func TestStatusWhenTheDaemonIsDown(t *testing.T) {
	d, opts := fixture(t)
	d.down = true
	var out statusOut
	if e := callTool(t, connect(t, opts), "status", nil, &out); e != "" || out.Running || !strings.Contains(out.Message, "flock daemon") {
		t.Fatalf("%q %+v", e, out)
	}
}

func TestSiteResourceAndDebugPrompt(t *testing.T) {
	_, opts := fixture(t)
	cs := connect(t, opts)
	ctx := context.Background()

	for _, uri := range []string{currentSiteURI, siteURIPrefix + "shop"} {
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || !strings.Contains(res.Contents[0].Text, `"url": "https://shop.test"`) {
			t.Fatalf("%s: %v %+v", uri, err, res)
		}
	}

	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "debug_site", Arguments: map[string]string{"problem": "checkout shows a 500"}})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{"checkout shows a 500", "https://shop.test", "## Recent errors", "## Recent mail"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt lacks %q:\n%s", want, text)
		}
	}
}

func TestHTMLText(t *testing.T) {
	got := htmlText("<div>Hello&nbsp;<b>you</b></div><script>x()</script><p>Line&amp;two</p>\n\n\n\n<p>end</p>")
	if got != "Hello\u00a0you\nLine&two\n\nend" {
		t.Fatalf("%q", got)
	}
	if got := truncate("ééé", 3); !strings.HasPrefix(got, "é\n") {
		t.Fatalf("truncate splits runes: %q", got)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
