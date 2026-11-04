package web

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func testConfig() Config {
	return Config{
		StorageDir: "/data/Application Support/caddy",
		LogFile:    "/data/web.log",
		HTTPPort:   80,
		HTTPSPort:  443,
		Sites: []Site{
			{Domain: "plain.test", Root: "/code/plain/public", FPM: []string{"unix//run/php85.sock"}},
			{Domain: "safe.test", Root: "/code/safe/public", FPM: []string{"unix//run/php84.sock"}, CertFile: "/c/safe.crt", KeyFile: "/c/safe.key"},
		},
	}
}

func TestCaddyfileAdaptsWithoutErrors(t *testing.T) {
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(testConfig())), nil); err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(testConfig()))
	}
}

func TestCaddyfileRoutesSitesAndSubdomains(t *testing.T) {
	cf := Caddyfile(testConfig())

	for _, want := range []string{
		"http://plain.test:80, http://*.plain.test:80, http://*.*.plain.test:80, http://*.*.*.plain.test:80, http://*.*.*.*.plain.test:80 {",
		"https://safe.test:443, https://*.safe.test:443, https://*.*.safe.test:443, https://*.*.*.safe.test:443, https://*.*.*.*.safe.test:443 {",
		"get_certificate flock",
		"redir https://{host}{uri} 308",
		"https://:443 {",
		"rewrite * /unlinked.html",
		"https://plain.test:443, https://*.plain.test:443, https://*.*.plain.test:443, https://*.*.*.plain.test:443, https://*.*.*.*.plain.test:443 {",
		`vars flock_site "plain"`,
		`php_fastcgi "unix//run/php84.sock"`,
		`storage file_system "/data/Application Support/caddy"`,
		"abort @remote",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("missing %q in:\n%s", want, cf)
		}
	}
}

func TestWithBindQualifiesListenAddresses(t *testing.T) {
	in := []byte(`{"apps":{"http":{"servers":{"srv0":{"listen":[":443"]}}}}}`)

	if got := string(withBind(in, "127.0.0.1")); !strings.Contains(got, `"127.0.0.1:443"`) {
		t.Fatalf("got %s", got)
	}
	if got := string(withBind(in, "")); got != string(in) {
		t.Fatalf("empty bind must not change config, got %s", got)
	}
}

func TestCaddyfileBalancesPHPCGIWorkersAndKeepsWindowsPaths(t *testing.T) {
	cfg := Config{
		StorageDir: `C:\Users\me\AppData\Roaming\Flock\caddy`,
		LogFile:    `C:\Users\me\AppData\Roaming\Flock\log\web.log`,
		HTTPPort:   80,
		HTTPSPort:  443,
		Sites: []Site{{
			Domain: "app.test", Root: `C:\code\app\public`,
			FPM:      []string{"127.0.0.1:19840", "127.0.0.1:19841"},
			CertFile: `C:\Users\me\AppData\Roaming\Flock\certs\app.test.crt`, KeyFile: `C:\Users\me\AppData\Roaming\Flock\certs\app.test.key`,
		}},
	}

	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	for _, want := range []string{
		`"dial":"127.0.0.1:19840"`, `"dial":"127.0.0.1:19841"`, `"policy":"least_conn"`,
		`"root":"C:/code/app/public"`,
		`"via":"flock"`,
		`"root":"C:/Users/me/AppData/Roaming/Flock/caddy"`,
	} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
}

func TestCaddyfileSendsXdebugRequestsToTheDebugPool(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = []Site{
		{Domain: "plain.test", Root: "/code/plain/public", FPM: []string{"unix//run/php85.sock"}, DebugFPM: []string{"unix//run/php85-xdebug.sock"}},
		{Domain: "debugged.test", Root: "/code/debugged/public", FPM: []string{"unix//run/php84.sock"}, DebugFPM: []string{"unix//run/php84-xdebug.sock"}, Debugging: true},
	}

	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	for _, want := range []string{
		`"dial":"unix//run/php85-xdebug.sock"`, `"dial":"unix//run/php85.sock"`, `XDEBUG_SESSION`, `XDEBUG_TRIGGER`,
		// With breakpoints, the site has no fast pool at all.
		`"dial":"unix//run/php84-xdebug.sock"`,
	} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
	if strings.Contains(string(json), `"dial":"unix//run/php84.sock"`) {
		t.Errorf("a site with breakpoints still uses its fast pool:\n%s", json)
	}
}

func TestCaddyfileProxiesFrankenPHPSitesButNotTheirXdebugRequests(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = []Site{
		{Domain: "franken.test", Root: "/code/franken/public", Proxy: "127.0.0.1:21000", DebugFPM: []string{"unix//run/php84-xdebug.sock"}},
		{Domain: "octane.test", Root: "/code/octane/public", Proxy: "127.0.0.1:21001"},
	}

	json, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(Caddyfile(cfg)), nil)
	if err != nil {
		t.Fatalf("%v\n%s", err, Caddyfile(cfg))
	}
	for _, want := range []string{`"dial":"127.0.0.1:21000"`, `"dial":"127.0.0.1:21001"`, `"dial":"unix//run/php84-xdebug.sock"`, `XDEBUG_TRIGGER`} {
		if !strings.Contains(string(json), want) {
			t.Errorf("missing %s in:\n%s", want, json)
		}
	}
}

func TestCaddyfileProxiesNamesToTheirServer(t *testing.T) {
	cfg := testConfig()
	cfg.Sites = append(cfg.Sites,
		Site{Domain: "next.test", Proxy: "http://127.0.0.1:3000"},
		Site{Domain: "s3.test", Proxy: "http://127.0.0.1:9000", CertFile: "/c/s3.crt", KeyFile: "/c/s3.key"},
		Site{Domain: "api.test", Proxy: "https://127.0.0.1:8443"},
	)
	cf := Caddyfile(cfg)
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cf), nil); err != nil {
		t.Fatalf("%v\n%s", err, cf)
	}

	for _, want := range []string{
		"http://next.test:80, http://*.next.test:80, http://*.*.next.test:80, http://*.*.*.next.test:80, http://*.*.*.*.next.test:80 {\n\timport flock_site\n\treverse_proxy \"http://127.0.0.1:3000\"\n",
		"https://s3.test:443, https://*.s3.test:443, https://*.*.s3.test:443, https://*.*.*.s3.test:443, https://*.*.*.*.s3.test:443 {\n\timport flock_site\n\ttls {\n\t\tget_certificate flock\n\t}\n\treverse_proxy \"http://127.0.0.1:9000\"\n",
		"\tvars {\n\t\tflock_site \"next\"\n\t\tflock_proxy \"1\"\n\t}\n",
		"\treverse_proxy \"https://127.0.0.1:8443\" {\n\t\theader_up Host {http.request.hostport}\n\t}\n",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("missing %q in:\n%s", want, cf)
		}
	}
	if !strings.Contains(cf, `vars flock_site "plain"`+"\n") || strings.Count(cf, "flock_proxy") != 2 {
		t.Errorf("only the unsecured proxies' HTTPS pages say they're proxies:\n%s", cf)
	}
	// Only the blocks that pass requests to a proxy's server show its page when the server is down:
	// next's and api's over HTTP, s3's over HTTPS. The PHP sites keep their plain errors.
	if n := strings.Count(cf, "handle_errors 502 {"); n != 3 || !strings.Contains(cf, "\t\t\tflock_down_upstream \"127.0.0.1:3000\"\n") ||
		!strings.Contains(cf, "\t\t\tflock_down_upstream \"https://127.0.0.1:8443\"\n") {
		t.Errorf("want the upstream-down page on the 3 proxies' serving blocks, got %d in:\n%s", n, cf)
	}
	if strings.Contains(cf, `root * ""`) {
		t.Errorf("a proxy has no document root:\n%s", cf)
	}
}
