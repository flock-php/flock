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

	"flock/internal/certs"
)

// currentCerts is what the `get_certificate flock` module serves from; Apply replaces it with each config.
var currentCerts atomic.Pointer[certSource]

func init() {
	caddy.RegisterModule(certModule{})
}

// certSource picks the certificate for a TLS handshake: a secured site's own certificate when it
// covers the name (domain and *.domain, the files Vite uses too), otherwise one issued for exactly
// that name, which is how a.b.app.test, aliases and the HTTPS catch-all get theirs.
//
// A name must belong to a suffix Flock has a root for: every root is name-constrained to its own
// suffix, so a leaf for a name outside it is signed at worst and rejected by every verifier at
// best. Answering "no certificate" for those names is what keeps the handshake fast.
type certSource struct {
	tld    string
	roots  *certs.Roots // the root of each suffix Flock answers for; nil means the one tld
	served string       // how those suffixes are named in the "no certificate" answer
	static []*tls.Certificate
	issue  func(name string) (*tls.Certificate, error)
}

func newCertSource(cfg Config) (*certSource, error) {
	s := &certSource{tld: strings.ToLower(cfg.TLD), roots: cfg.Roots, issue: cfg.Issue}
	suffixes := s.suffixes()
	patterns := make([]string, 0, len(suffixes))
	for _, suffix := range suffixes {
		patterns = append(patterns, "*."+suffix)
	}
	s.served = strings.Join(patterns, ", ")
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

// suffixes are the suffixes HTTPS answers names under: the ones with a root, and the config's
// TLD when it names no roots (the single-suffix setup).
func (s *certSource) suffixes() []string {
	if s.roots != nil {
		if got := s.roots.Suffixes(); len(got) > 0 {
			return got
		}
	}

	return []string{s.tld}
}

// serves reports whether a loaded root covers name.
func (s *certSource) serves(name string) bool {
	if s.roots != nil {
		_, ok := s.roots.For(name)
		return ok
	}
	return name == s.tld || strings.HasSuffix(name, "."+s.tld)
}

func (s *certSource) certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.TrimSuffix(strings.ToLower(hello.ServerName), ".")
	switch {
	case s == nil:
		return nil, fmt.Errorf("no certificates configured")
	case hello.Conn != nil && !loopback(hello.Conn.RemoteAddr()):
		// Remote clients are refused per request anyway; don't issue certificates for them.
		return nil, fmt.Errorf("not a local client")
	case name == "" || !s.serves(name):
		return nil, fmt.Errorf("no certificate for %q: only %s is served", name, s.served)
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
