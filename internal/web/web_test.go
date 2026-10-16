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
