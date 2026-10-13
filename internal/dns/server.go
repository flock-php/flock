// Package dns answers every query under the local TLD (e.g. *.test) with loopback.
//
// On macOS it is wired up via /etc/resolver/<tld> with a non-privileged port,
// so it never needs root. Windows uses the hosts file instead (like Herd) and
// does not run this server.
package dns

import (
	"fmt"
	"net"
	"strings"

	mdns "github.com/miekg/dns"
)

const ttl = 60

// Server is a tiny authoritative DNS server for a single local TLD.
type Server struct {
	tld  string
	addr string
	udp  *mdns.Server
	tcp  *mdns.Server
}

// New creates a server for tld (without dots, e.g. "test") listening on addr (e.g. "127.0.0.1:53535").
func New(addr, tld string) *Server {
	return &Server{tld: strings.Trim(strings.ToLower(tld), "."), addr: addr}
}

// Start binds UDP and TCP on the same port and serves in the background.
func (s *Server) Start() error {
	pc, err := net.ListenPacket("udp", s.addr)
	if err != nil {
		return fmt.Errorf("dns udp listen: %w", err)
	}

	// Reuse the port UDP actually got, so ":0" works in tests.
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		_ = pc.Close()
		return fmt.Errorf("dns tcp listen: %w", err)
	}

	s.addr = pc.LocalAddr().String()
	s.udp = &mdns.Server{PacketConn: pc, Handler: mdns.HandlerFunc(s.handle)}
	s.tcp = &mdns.Server{Listener: ln, Handler: mdns.HandlerFunc(s.handle)}

	go func() { _ = s.udp.ActivateAndServe() }()
	go func() { _ = s.tcp.ActivateAndServe() }()

	return nil
}

// Addr is the bound address (resolved after Start).
func (s *Server) Addr() string {
	return s.addr
}

// TLD is the domain this server answers for.
func (s *Server) TLD() string {
	return s.tld
}

// Shutdown stops both listeners.
func (s *Server) Shutdown() error {
	var firstErr error
	for _, srv := range []*mdns.Server{s.udp, s.tcp} {
		if srv == nil {
			continue
		}
		if err := srv.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

func (s *Server) handle(w mdns.ResponseWriter, req *mdns.Msg) {
	resp := new(mdns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true

	for _, q := range req.Question {
		if !s.owns(q.Name) {
			resp.Rcode = mdns.RcodeRefused
			continue
		}

		hdr := mdns.RR_Header{Name: q.Name, Class: mdns.ClassINET, Ttl: ttl}
		switch q.Qtype {
		case mdns.TypeA:
			hdr.Rrtype = mdns.TypeA
			resp.Answer = append(resp.Answer, &mdns.A{Hdr: hdr, A: net.IPv4(127, 0, 0, 1)})
		case mdns.TypeAAAA:
			hdr.Rrtype = mdns.TypeAAAA
			resp.Answer = append(resp.Answer, &mdns.AAAA{Hdr: hdr, AAAA: net.IPv6loopback})
		}
	}

	_ = w.WriteMsg(resp)
}

func (s *Server) owns(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")

	return name == s.tld || strings.HasSuffix(name, "."+s.tld)
}
