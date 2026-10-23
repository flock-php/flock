package web

import (
	"crypto/tls"
	"net"
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

	if got := string(withBind(in, []string{"127.0.0.1"})); !strings.Contains(got, `"listen":["127.0.0.1:443"]`) {
		t.Fatalf("got %s", got)
	}
	if got := string(withBind(in, []string{"127.0.0.1", "100.64.0.7"})); !strings.Contains(got, `"listen":["127.0.0.1:443","100.64.0.7:443"]`) {
		t.Fatalf("with a Listen address: got %s", got)
	}
	if got := string(withBind(in, nil)); got != string(in) {
		t.Fatalf("no bind must not change config, got %s", got)
	}
	if got := binds(Config{Listen: []string{"100.64.0.7"}}); got != nil {
		t.Fatalf("binds without Bind: %v, want every address (nil)", got)
	}
}

// Requests reaching a Listen address are served whichever client sent them; elsewhere only
// loopback clients are.
func TestCaddyfileLetsRequestsOnListenAddressesThrough(t *testing.T) {
	cfg := testConfig()
	cfg.Listen = []string{"100.64.0.7", "192.168.1.5"}
	cf := Caddyfile(cfg)
	for _, want := range []string{"not client_ip 127.0.0.0/8 ::1", "not vars {http.request.local.host} 100.64.0.7 192.168.1.5"} {
		if !strings.Contains(cf, want) {
			t.Errorf("missing %q in:\n%s", want, cf)
		}
	}
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(cf), nil); err != nil {
		t.Fatalf("adapt: %v", err)
	}
	if cf := Caddyfile(testConfig()); strings.Contains(cf, "http.request.local.host") {
		t.Errorf("no Listen addresses, but the exemption is there:\n%s", cf)
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

// addrConn is a connection with just the addresses a TLS handshake looks at.
type addrConn struct {
	net.Conn
	local, remote net.Addr
}

func (c addrConn) LocalAddr() net.Addr  { return c.local }
func (c addrConn) RemoteAddr() net.Addr { return c.remote }

// Another machine gets a certificate only when it came in on a Listen address.
func TestCertificatesForOtherMachinesOnlyOnListenAddresses(t *testing.T) {
	s := &certSource{tld: "test", listen: []string{"100.64.0.7"}, issue: func(string) (*tls.Certificate, error) { return &tls.Certificate{}, nil }}
	for _, c := range []struct {
		local, remote string
		ok            bool
	}{
		{"127.0.0.1", "127.0.0.1", true},
		{"100.64.0.7", "100.64.0.9", true},
		{"192.168.1.5", "192.168.1.9", false}, // the wildcard bind of macOS, reached on another address
	} {
		conn := addrConn{local: &net.TCPAddr{IP: net.ParseIP(c.local), Port: 443}, remote: &net.TCPAddr{IP: net.ParseIP(c.remote), Port: 50000}}
		_, err := s.certificate(&tls.ClientHelloInfo{ServerName: "app.test", Conn: conn})
		if (err == nil) != c.ok {
			t.Errorf("%s from %s: err %v, want ok %v", c.local, c.remote, err, c.ok)
		}
	}
}
