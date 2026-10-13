package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"sync/atomic"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// currentCerts is what the `get_certificate flock` module serves from; Apply replaces it with each config.
var currentCerts atomic.Pointer[certSource]

func init() {
	caddy.RegisterModule(certModule{})
}

// certSource picks the certificate for a TLS handshake: a secured site's own certificate when it
// covers the name (domain and *.domain, the files Vite uses too), otherwise one issued for exactly
// that name, which is how a.b.app.test, aliases and the HTTPS catch-all get theirs.
type certSource struct {
	tld    string
	static []*tls.Certificate
	issue  func(name string) (*tls.Certificate, error)
}

func newCertSource(cfg Config) (*certSource, error) {
	s := &certSource{tld: strings.ToLower(cfg.TLD), issue: cfg.Issue}
	for _, site := range cfg.Sites {
		if site.CertFile == "" {
			continue
		}
		c, err := tls.LoadX509KeyPair(site.CertFile, site.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("certificate for %s: %w", site.Domain, err)
		}
		if c.Leaf == nil {
			if c.Leaf, err = x509.ParseCertificate(c.Certificate[0]); err != nil {
				return nil, fmt.Errorf("certificate for %s: %w", site.Domain, err)
			}
		}
		s.static = append(s.static, &c)
	}
	return s, nil
}

func (s *certSource) certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.TrimSuffix(strings.ToLower(hello.ServerName), ".")
	switch {
	case s == nil:
		return nil, fmt.Errorf("no certificates configured")
	case hello.Conn != nil && !loopback(hello.Conn.RemoteAddr()):
		// Remote clients are refused per request anyway; don't issue certificates for them.
		return nil, fmt.Errorf("not a local client")
	case name == "" || (name != s.tld && !strings.HasSuffix(name, "."+s.tld)):
		return nil, fmt.Errorf("no certificate for %q: only *.%s is served", name, s.tld)
	}

	for _, c := range s.static {
		if c.Leaf.VerifyHostname(name) == nil {
			return c, nil
		}
	}
	if s.issue == nil {
		return nil, fmt.Errorf("no certificate for %q", name)
	}
	return s.issue(name)
}

func loopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return true // e.g. QUIC: Caddy's listener only accepts what it bound
	}
	return tcp.IP.IsLoopback()
}

// certModule is Caddy's `get_certificate flock`.
type certModule struct{}

func (certModule) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "tls.get_certificate.flock", New: func() caddy.Module { return new(certModule) }}
}

func (*certModule) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // the module name
	if d.NextArg() {
		return d.ArgErr()
	}
	return nil
}

// GetCertificate implements certmagic.Manager.
func (*certModule) GetCertificate(_ context.Context, hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return currentCerts.Load().certificate(hello)
}
