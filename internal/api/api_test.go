package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/config"
	"flock/internal/dumps"
)

// fakeBackend applies mutations to config.json in root, like the daemon, without running services.
type fakeBackend struct {
	root  string
	sites []SiteStatus
}

func (f *fakeBackend) Status() Status      { return Status{Sites: f.sites} }
func (f *fakeBackend) Reload() error       { return nil }
func (f *fakeBackend) Root() string        { return f.root }
func (f *fakeBackend) Services() []Service { return []Service{} }
func (f *fakeBackend) Mutate(change func(*config.Config) error) error {
	cfg, err := config.Load(f.root)
	if err != nil {
		return err
	}
	if err := change(cfg); err != nil {
		return err
	}
	return cfg.Save(f.root)
}

func (f *fakeBackend) config(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func serve(t *testing.T, b Backend, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewHandler(b, token))
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, method, url, token, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestTokenIsRequiredWhenConfigured(t *testing.T) {
	srv := serve(t, &fakeBackend{root: t.TempDir()}, "secret")

	if resp, _ := call(t, "GET", srv.URL+"/v1/status", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/status", "wrong", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/status", "secret", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("right token: %d", resp.StatusCode)
	}
}

func TestSettingsOnlyChangeGivenKeys(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	if resp, body := call(t, "POST", srv.URL+"/v1/settings", "", `{"dumps":true}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	if cfg := b.config(t); !cfg.Dumps || cfg.Xdebug {
		t.Fatalf("got %+v", cfg)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/settings", "", `{"default_php":"5.6"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("uninstalled default PHP must be rejected, got %d", resp.StatusCode)
	}
}

func TestMailEndpoints(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mail", "app", "1-000001.eml")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	raw := "From: a@app.test\r\nSubject: Hi\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/html\r\n\r\n<b>hi</b>\r\n" +
		"--b\r\nContent-Type: text/csv\r\nContent-Disposition: attachment; filename=\"r.csv\"\r\n\r\na,b\r\n--b--\r\n"
	_ = os.WriteFile(path, []byte(raw), 0o600)
	srv := serve(t, &fakeBackend{root: root}, "")

	_, list := call(t, "GET", srv.URL+"/v1/mail", "", "")
	if !strings.Contains(list, `"id":"1-000001"`) {
		t.Fatalf("list: %s", list)
	}

	_, detail := call(t, "GET", srv.URL+"/v1/mail/1-000001", "", "")
	var d struct {
		HTML        string `json:"html"`
		Attachments []struct {
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil || !strings.Contains(d.HTML, "<b>hi</b>") || len(d.Attachments) != 1 {
		t.Fatalf("detail: %s", detail)
	}

	resp, body := call(t, "GET", srv.URL+"/v1/mail/1-000001/attachments/0", "", "")
	if strings.TrimSpace(body) != "a,b" || !strings.Contains(resp.Header.Get("Content-Disposition"), "r.csv") {
		t.Fatalf("attachment: %q %v", body, resp.Header)
	}

	if resp, _ := call(t, "GET", srv.URL+"/v1/mail/nope", "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing message: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/mail", "", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("clear: %d", resp.StatusCode)
	}
	if _, list := call(t, "GET", srv.URL+"/v1/mail", "", ""); strings.TrimSpace(list) != "[]" {
		t.Fatalf("after clear: %s", list)
	}
}

func TestDumpsAreAttributedToSites(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Dir(dumps.File(root)), 0o700)
	lines := `{"id":"1","time":"2026-10-13T12:00:00.000+00:00","source":{"file":"/code/shop/app/Http/Kernel.php","line":3,"name":"Kernel.php"},"text":"a","html":""}
{"id":"2","time":"2026-10-13T12:00:01.000+00:00","request":{"method":"GET","uri":"https://api.blog.test/x"},"text":"b","html":""}
`
	_ = os.WriteFile(dumps.File(root), []byte(lines), 0o600)
	b := &fakeBackend{root: root, sites: []SiteStatus{{Name: "shop", Domain: "shop.test", Path: "/code/shop"}, {Name: "blog", Domain: "blog.test", Path: "/code/blog"}}}
	srv := serve(t, b, "")

	_, body := call(t, "GET", srv.URL+"/v1/dumps", "", "")
	var entries []dumps.Entry
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Site != "blog.test" || entries[1].Site != "shop.test" {
		t.Fatalf("got %+v", entries)
	}
}

func TestLogsEndpoints(t *testing.T) {
	root := t.TempDir()
	site := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "log"), 0o700)
	_ = os.WriteFile(filepath.Join(root, "log", "web.log"), []byte("one\ntwo\nthree\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(site, "storage", "logs"), 0o755)
	_ = os.WriteFile(filepath.Join(site, "storage", "logs", "laravel.log"), []byte("[2026-10-13 10:00:00] local.ERROR: Boom\n"), 0o644)
	srv := serve(t, &fakeBackend{root: root, sites: []SiteStatus{{Name: "shop", Path: site}}}, "")

	if _, body := call(t, "GET", srv.URL+"/v1/logs", "", ""); !strings.Contains(body, `"services":["web"]`) || !strings.Contains(body, `"sites":["shop"]`) {
		t.Fatalf("index: %s", body)
	}
	if _, body := call(t, "GET", srv.URL+"/v1/logs/services/web?lines=2", "", ""); body != "two\nthree\n" {
		t.Fatalf("tail: %q", body)
	}
	if resp, _ := call(t, "GET", srv.URL+"/v1/logs/services/..%2Fconfig", "", ""); resp.StatusCode == http.StatusOK {
		t.Fatal("path traversal must be rejected")
	}
	if _, body := call(t, "GET", srv.URL+"/v1/logs/sites/shop", "", ""); !strings.Contains(body, `"level":"error"`) {
		t.Fatalf("site log: %s", body)
	}
}

func TestSiteAndParkedEndpoints(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")
	project := t.TempDir()

	if resp, body := call(t, "POST", srv.URL+"/v1/sites", "", `{"path":"`+project+`","name":"My App"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("link: %d %s", resp.StatusCode, body)
	}
	if site, ok := b.config(t).FindSite("my-app"); !ok || site.Path != project {
		t.Fatalf("site not linked: %+v", b.config(t).Sites)
	}

	if resp, body := call(t, "PATCH", srv.URL+"/v1/sites/my-app", "", `{"secured":true,"node":"20"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: %d %s", resp.StatusCode, body)
	}
	if site, _ := b.config(t).FindSite("my-app"); !site.Secured || site.Node != "20" {
		t.Fatalf("patch not applied: %+v", site)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/sites/my-app", "", `{"php":"5.6"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("uninstalled PHP must be rejected, got %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/sites", "", `{"path":"relative/dir"}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("relative path must be rejected, got %d", resp.StatusCode)
	}

	parent := filepath.Dir(project)
	if resp, body := call(t, "POST", srv.URL+"/v1/parked", "", `{"path":"`+parent+`"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("park: %d %s", resp.StatusCode, body)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/sites/"+filepath.Base(t.TempDir()), "", ""); resp.StatusCode != http.StatusConflict {
		t.Fatalf("unlinking a parked-only site must be a conflict, got %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/sites/my-app", "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("unlink: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "DELETE", srv.URL+"/v1/parked", "", `{"path":"`+parent+`"}`); resp.StatusCode != http.StatusOK || len(b.config(t).Parked) != 0 {
		t.Fatalf("forget: %d %v", resp.StatusCode, b.config(t).Parked)
	}
}

func TestServiceStartStopAndPorts(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	if resp, _ := call(t, "POST", srv.URL+"/v1/services/postgres/stop", "", ""); resp.StatusCode != http.StatusOK || b.config(t).Services.Running("postgres") {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/services/postgres/start", "", ""); resp.StatusCode != http.StatusOK || !b.config(t).Services.Running("postgres") {
		t.Fatalf("start: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "POST", srv.URL+"/v1/services/web/stop", "", ""); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("web must not be stoppable, got %d", resp.StatusCode)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/services/redis", "", `{"port":6380}`); resp.StatusCode != http.StatusOK || b.config(t).Ports.Redis != 6380 {
		t.Fatalf("port: %d", resp.StatusCode)
	}
	if resp, _ := call(t, "PATCH", srv.URL+"/v1/services/redis", "", `{"port":80}`); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("privileged port must be rejected, got %d", resp.StatusCode)
	}
}

func TestConfigView(t *testing.T) {
	b := &fakeBackend{root: t.TempDir()}
	srv := serve(t, b, "")

	_, body := call(t, "GET", srv.URL+"/v1/config", "", "")
	for _, want := range []string{`"tld":"test"`, `"php":[]`, `"node":[]`, `"parked":[]`, `"redis":6379`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
}
