package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flock/internal/api"
	"flock/internal/config"
)

// installRustFS records RustFS as installed on the port of srv, and stopped, so the daemon doesn't
// start it: srv answers in its place.
func installRustFS(t *testing.T, c *config.Config, srv string) {
	t.Helper()
	u, err := url.Parse(srv)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	c.Services.SetExtra("rustfs", config.Extra{Version: "1.0.6", Port: port})
	c.Services.SetRunning("rustfs", false)
}

func service(t *testing.T, client *api.Client, id string) api.Service {
	t.Helper()
	services, err := client.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range services {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no service %s in %+v", id, services)
	return api.Service{}
}

func TestRustFSIsServedOverHTTPSAtItsName(t *testing.T) {
	s3 := upstream(t)
	root, cfg := testRoot(t, func(c *config.Config) { installRustFS(t, c, s3.URL) })
	client := startDaemon(t, root)

	https := strconv.Itoa(cfg.Ports.HTTPS)
	rustfs := service(t, client, "rustfs")
	if !strings.Contains(rustfs.Env, "\nAWS_ENDPOINT=https://rustfs.test:"+https+"\n") ||
		!strings.Contains(rustfs.Env, "\nAWS_URL=https://rustfs.test:"+https+"/laravel\n") {
		t.Errorf("env: %q", rustfs.Env)
	}
	if rustfs.URL != "https://rustfs-console.test:"+https+"/rustfs/console/" {
		t.Errorf("url: %q", rustfs.URL)
	}

	// The proxies list only the user's.
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Proxies) != 0 {
		t.Errorf("proxies: %+v", status.Proxies)
	}

	// Over HTTPS with Flock's CA, the Host header unchanged: S3 signatures cover it.
	pem, err := os.ReadFile(filepath.Join(root, "certs", "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pem)
	web := "127.0.0.1:" + https
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, web)
		},
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Get("https://rustfs.test:" + https + "/laravel/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "rustfs.test:"+https+" https" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestAProxyKeepsItsNameFromRustFS(t *testing.T) {
	s3, dev := upstream(t), upstream(t)
	root, cfg := testRoot(t, func(c *config.Config) {
		installRustFS(t, c, s3.URL)
		c.Proxies = []config.Proxy{{Name: "rustfs", Upstream: dev.URL}}
	})
	client := startDaemon(t, root)

	port := strings.TrimPrefix(s3.URL, "http://127.0.0.1:")
	rustfs := service(t, client, "rustfs")
	if !strings.Contains(rustfs.Env, "\nAWS_ENDPOINT=http://127.0.0.1:"+port+"\n") {
		t.Errorf("env: %q", rustfs.Env)
	}
	// The console's name is free.
	if rustfs.URL != "https://rustfs-console.test:"+strconv.Itoa(cfg.Ports.HTTPS)+"/rustfs/console/" {
		t.Errorf("url: %q", rustfs.URL)
	}

	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Proxies) != 1 || status.Proxies[0].Problem != "" {
		t.Fatalf("proxies: %+v", status.Proxies)
	}
	warned := false
	for _, w := range status.Warnings {
		warned = warned || strings.HasPrefix(w, "rustfs: rustfs.test is served by a site or proxy")
	}
	if !warned {
		t.Errorf("no warning about the name in %q", status.Warnings)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTP)+"/", nil)
	req.Host = "rustfs.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "rustfs.test http" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestRustFSKeepsLoopbackUntilTheWebServerStarts(t *testing.T) {
	retryInterval = 100 * time.Millisecond
	t.Cleanup(func() { retryInterval = 15 * time.Second })

	s3 := upstream(t)
	root, cfg := testRoot(t, func(c *config.Config) { installRustFS(t, c, s3.URL) })
	blocker, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(cfg.Ports.HTTPS))
	if err != nil {
		t.Fatal(err)
	}
	client := startDaemon(t, root)

	// Nothing serves https://rustfs.test yet.
	port := strings.TrimPrefix(s3.URL, "http://127.0.0.1:")
	rustfs := service(t, client, "rustfs")
	if !strings.Contains(rustfs.Env, "\nAWS_ENDPOINT=http://127.0.0.1:"+port+"\n") {
		t.Errorf("env: %q", rustfs.Env)
	}
	if strings.Contains(rustfs.URL, "rustfs-console.test") {
		t.Errorf("url: %q", rustfs.URL)
	}

	blocker.Close()
	want := "\nAWS_ENDPOINT=https://rustfs.test:" + strconv.Itoa(cfg.Ports.HTTPS) + "\n"
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(service(t, client, "rustfs").Env, want) {
		if time.Now().After(deadline) {
			t.Fatalf("the web server started, but the env is %q", service(t, client, "rustfs").Env)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
