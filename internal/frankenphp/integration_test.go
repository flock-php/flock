package frankenphp

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/supervisor"
)

// TestIntegrationServeClassicAndWorker installs a real FrankenPHP package (a directory or archive
// from the binaries repository's FrankenPHP workflow) and serves a site from it, classic and in
// worker mode with a stand-in for Laravel Octane's worker script. Run with:
//
//	FLOCK_FRANKENPHP_PACKAGE=/path/to/frankenphp-php8.4-macos-arm64 go test ./internal/frankenphp -run Integration -v
func TestIntegrationServeClassicAndWorker(t *testing.T) {
	pkg := os.Getenv("FLOCK_FRANKENPHP_PACKAGE")
	if testing.Short() || pkg == "" {
		t.Skip("set FLOCK_FRANKENPHP_PACKAGE to a FrankenPHP package to run")
	}
	root := t.TempDir()
	minor, info, err := InstallFrom(root, pkg, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("FrankenPHP %s with PHP %s", info.Version, info.PHP)

	project := t.TempDir()
	public := filepath.Join(project, "public")
	_ = os.MkdirAll(public, 0o755)
	_ = os.WriteFile(filepath.Join(public, "index.php"), []byte(`<?php echo "classic ", PHP_VERSION, " https=", $_SERVER['HTTPS'] ?? 'off', " host=", $_SERVER['HTTP_HOST'];`), 0o644)
	_ = os.WriteFile(filepath.Join(public, "style.css"), []byte("body{}"), 0o644)
	// What Octane's bin/frankenphp-worker.php boils down to: keep state between requests.
	_ = os.MkdirAll(filepath.Dir(OctaneWorker(project)), 0o755)
	_ = os.WriteFile(OctaneWorker(project), []byte(`<?php
$n = 0;
while (frankenphp_handle_request(function () use (&$n) {
    echo "worker ", ++$n, " base=", basename($_SERVER['APP_BASE_PATH']), " https=", $_SERVER['HTTPS'] ?? 'off';
})) {
    gc_collect_cycles();
}
`), 0o644)

	sup := supervisor.New()
	defer sup.StopAll()
	get := func(port int, path string) string {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			req, _ := http.NewRequest("GET", "http://127.0.0.1:"+itoa(port)+path, nil)
			req.Host = "app.test"
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return string(body)
			}
			if time.Now().After(deadline) {
				logs, _ := os.ReadFile(filepath.Join(root, "log", "frankenphp-"+itoa(port)+".log"))
				t.Fatalf("no answer on %d: %v\n%s", port, err, logs)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	classic, err := Spec(root, Site{Name: "21950", Path: project, Root: public, Minor: minor, Port: 21950})
	if err != nil {
		t.Fatal(err)
	}
	sup.Ensure(classic)
	if body := get(21950, "/"); !strings.HasPrefix(body, "classic "+info.PHP) || !strings.Contains(body, "https=off") || !strings.Contains(body, "host=app.test") {
		t.Errorf("classic: %q", body)
	}
	if body := get(21950, "/style.css"); body != "body{}" {
		t.Errorf("static file: %q", body)
	}

	worker, err := Spec(root, Site{Name: "21951", Path: project, Root: public, Minor: minor, Port: 21951, Octane: true, Secured: true})
	if err != nil {
		t.Fatal(err)
	}
	sup.Ensure(worker)
	first, second := get(21951, "/"), get(21951, "/some/route")
	if !strings.HasPrefix(first, "worker ") || !strings.Contains(first, "https=on") || !strings.Contains(first, "base="+filepath.Base(project)) {
		t.Errorf("worker: %q", first)
	}
	if first == second {
		t.Errorf("the worker kept no state between requests: %q, %q", first, second)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
