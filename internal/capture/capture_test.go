package capture

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/dumps"
	"flock/internal/php"
)

func TestStoreTrimsAndLists(t *testing.T) {
	root := t.TempDir()
	for i := range MaxEvents + MaxEvents/5 + 1 {
		kind := "query"
		if i%2 == 1 {
			kind = "log"
		}
		if err := Append(root, Event{Kind: kind, Message: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}

	if n := Count(root); n != MaxEvents {
		t.Errorf("Count = %d, want %d", n, MaxEvents)
	}
	all, err := List(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != MaxEvents || all[0].Message != fmt.Sprint(MaxEvents+MaxEvents/5) {
		t.Fatalf("got %d events, newest %q", len(all), all[0].Message)
	}
	logs, _ := List(root, 3, "log")
	if len(logs) != 3 || logs[0].Kind != "log" || logs[0].ID == "" {
		t.Errorf("filtered list: %+v", logs)
	}

	if err := Clear(root); err != nil {
		t.Fatal(err)
	}
	if n := Count(root); n != 0 {
		t.Errorf("after Clear: %d", n)
	}
}

func TestCollectorStoresDumpsAndEvents(t *testing.T) {
	root := t.TempDir()
	c, err := Listen(root, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	conn, err := net.Dial("tcp", c.Addr())
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"kind":"dump_header","html":"<script>header</script>"}`,
		`{"kind":"dump","time":"2026-10-18T09:40:07.123456+00:00","group":"g1","request":{"method":"GET","uri":"https://shop.test/"},"command_line":null,"label":"x","source":{"file":"/p/routes/web.php","line":3,"name":"web.php"},"text":"1","html":"<pre>1</pre>"}`,
		`{"kind":"query","time":"2026-10-18T09:40:07.2+00:00","group":"g1","sql":"select ?","bindings":[1],"connection":"sqlite","duration_ms":0.5}`,
		`{"kind":"log","group":"g1","level":"warning","message":"careful","context":{"user_id":1}}`,
		`{"kind":"mystery","group":"g1"}`,
		`not json`,
	}
	if _, err := conn.Write([]byte(strings.Join(lines, "\n") + "\n")); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for Count(root) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	events, _ := List(root, 0)
	if len(events) != 2 || events[1].SQL != "select ?" || events[0].Level != "warning" || string(events[0].Context) != `{"user_id":1}` {
		t.Fatalf("events: %+v", events)
	}
	ds, _ := dumps.List(root)
	if len(ds) != 1 || ds[0].Group != "g1" || ds[0].Label == nil || *ds[0].Label != "x" || ds[0].Request.URI != "https://shop.test/" {
		t.Fatalf("dumps: %+v", ds)
	}
	if h, _ := dumps.Header(root); h != "<script>header</script>" {
		t.Errorf("header: %q", h)
	}
}

func TestSyncWritesTheAgentIniWhereTheExtensionIs(t *testing.T) {
	root := t.TempDir()
	for _, minor := range []string{"8.4", "8.5"} {
		writeFile(t, php.Binary(root, minor), "") // php.exe on Windows
		writeFile(t, php.IniPath(root, minor), "memory_limit = 1G\nauto_prepend_file = \"/app/prepend.php\"\n")
	}
	writeFile(t, php.ExtensionPath(root, "8.5", Extension), "")
	agentIni := func(minor string) string {
		data, _ := os.ReadFile(filepath.Join(php.Dir(root, minor), "conf.d", iniName))
		return string(data)
	}

	on := Settings{On: true, Addr: "127.0.0.1:9913", Off: []string{"view"}, DumpsInPage: true}
	unsupported, err := Sync(root, on)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsupported) != 1 || unsupported[0] != "8.4" {
		t.Errorf("unsupported: %v", unsupported)
	}
	ini := agentIni("8.5")
	for _, want := range []string{
		`extension = "` + php.ExtensionPath(root, "8.5", Extension) + `"`,
		`auto_prepend_file = "` + AgentPath(root) + `"`,
		`flock.agent = "127.0.0.1:9913"`,
		`flock.capture = "dump,query,log,job,request"`,
		`flock.dumps_in_page = 1`,
		`flock.user_prepend = "/app/prepend.php"`,
	} {
		if !strings.Contains(ini, want) {
			t.Errorf("ini lacks %s:\n%s", want, ini)
		}
	}
	if agentIni("8.4") != "" {
		t.Error("8.4 has no extension but got the agent")
	}
	if data, _ := os.ReadFile(AgentPath(root)); string(data) != string(agentSource) {
		t.Error("agent not extracted")
	}

	// Changed settings change the pools' environment, so they restart and read the new ini; so
	// does a build that gains the extension (a rebuilt package of the same PHP version).
	if a, b := on.Env(root), (Settings{On: true, Addr: on.Addr}).Env(root); len(a) != 1 || a[0] == b[0] {
		t.Errorf("Env doesn't follow the settings: %v %v", a, b)
	}
	before := on.Env(root)
	writeFile(t, php.ExtensionPath(root, "8.4", Extension), "")
	if after := on.Env(root); after[0] == before[0] {
		t.Error("Env doesn't change when a build gains the extension")
	}

	if _, err := Sync(root, Settings{}); err != nil {
		t.Fatal(err)
	}
	if agentIni("8.5") != "" {
		t.Error("the agent stays loaded with capture off")
	}
}

// TestAgentInPHP runs the real agent with PHP and the extension when FLOCK_TEST_PHP and
// FLOCK_TEST_OTEL point at them (the extension isn't part of the repository).
func TestAgentInPHP(t *testing.T) {
	phpBin, ext := os.Getenv("FLOCK_TEST_PHP"), os.Getenv("FLOCK_TEST_OTEL")
	if phpBin == "" || ext == "" {
		t.Skip("set FLOCK_TEST_PHP and FLOCK_TEST_OTEL to run the agent")
	}
	root := t.TempDir()
	c, err := Listen(root, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := extract(root); err != nil {
		t.Fatal(err)
	}

	// A stand-in for Laravel's application and dispatcher, and for Symfony's VarDumper.
	app := filepath.Join(root, "app.php")
	writeFile(t, app, `<?php
namespace Illuminate\Foundation { class Application implements \ArrayAccess {
    public $listeners = [];
    public function boot() {}
    public function bound($k) { return $k === 'events'; }
    public function offsetGet($k): mixed { return $this; }
    public function offsetExists($k): bool { return true; }
    public function offsetSet($k, $v): void {}
    public function offsetUnset($k): void {}
    public function listen($event, $listener) { $this->listeners[$event][] = $listener; }
    public function dispatch($event, $payload) {
        foreach ($this->listeners[$event] ?? [] as $l) { $l(...$payload); }
        if (str_starts_with($event, 'composing: ')) { foreach ($this->listeners['composing: *'] ?? [] as $l) { $l($event, $payload); } }
    }
} }
namespace Illuminate\Database\Events { class QueryExecuted {
    public function __construct(public $sql, public $bindings, public $time, public $connectionName) {}
} }
namespace Illuminate\View { class View {
    public function __construct(public $name) {}
    public function name() { return $this->name; }
    public function getPath() { return '/views/'.$this->name.'.blade.php'; }
} }
namespace {
    $app = new Illuminate\Foundation\Application();
    $app->boot();
    $app->dispatch('Illuminate\Database\Events\QueryExecuted', [new Illuminate\Database\Events\QueryExecuted('select ?', [new DateTimeImmutable('2026-01-02 03:04:05'), 7], 1.5, 'sqlite')]);
    // An app view is captured; Laravel's error page components aren't.
    foreach (['welcome', 'laravel-exceptions-renderer::components.layout', 'laravel-exceptions::404'] as $name) {
        $app->dispatch('composing: '.$name, [new Illuminate\View\View($name)]);
    }
    echo "ok\n";
}
`)
	ini := ini(root, ext, "", Settings{On: true, Addr: c.Addr()})
	args := []string{"-n"}
	for _, l := range strings.Split(strings.TrimSpace(ini), "\n") {
		if !strings.HasPrefix(l, ";") {
			args = append(args, "-d", strings.ReplaceAll(strings.Replace(l, " = ", "=", 1), `"`, ""))
		}
	}
	out, err := execPHP(phpBin, append(args, app)...)
	if err != nil || strings.TrimSpace(out) != "ok" {
		t.Fatalf("php: %v\n%s", err, out)
	}

	deadline := time.Now().Add(2 * time.Second)
	for Count(root) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // anything the agent shouldn't have sent arrives too
	events, _ := List(root, 0)
	if len(events) != 2 || events[0].Kind != "view" || events[0].View != "welcome" {
		t.Fatalf("events: %+v", events)
	}
	e := events[1]
	b, _ := json.Marshal(e.Bindings)
	app, _ = filepath.EvalSymlinks(app) // PHP reports the resolved path (macOS: /var is /private/var)
	if e.Kind != "query" || e.SQL != "select ?" || string(b) != `["2026-01-02 03:04:05.000000+00:00",7]` || e.DurationMS != 1.5 ||
		e.CommandLine == nil || e.Source == nil || e.Source.File != app || e.Group == "" {
		raw, _ := os.ReadFile(File(root))
		t.Errorf("event: %+v (bindings %s)\n%s", e, b, raw)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
