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
			{Domain: "plain.test", Root: "/code/plain/public", FPM: "unix//run/php85.sock"},
			{Domain: "safe.test", Root: "/code/safe/public", FPM: "unix//run/php84.sock", CertFile: "/c/safe.crt", KeyFile: "/c/safe.key"},
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
		"http://plain.test:80, http://*.plain.test:80 {",
		"https://safe.test:443, https://*.safe.test:443 {",
		`tls "/c/safe.crt" "/c/safe.key"`,
		"redir https://{host}{uri} 308",
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
