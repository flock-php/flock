// Package dns answers every query under the local TLD (e.g. *.test) with loopback, or with the
// address it reached when that's one other machines use (config's listen addresses).
//
// On macOS it is wired up via /etc/resolver/<tld> with a non-privileged port, so it never needs
// root. On Windows it listens on port 53 (Windows can't send queries to another port) and an NRPT
// rule (see nrpt_windows.go) sends the TLD to it; the hosts file stays as the fallback.
package dns

import (
	"fmt"
	"net"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"
)

const ttl = 60

// Server is a tiny authoritative DNS server for a single local TLD.
type Server struct {
	tld     string
	addr    string
	also    []string
	servers []*mdns.Server

	mu      sync.Mutex
	watched map[string]bool // name → queried since Watch
}

// New creates a server for tld (without dots, e.g. "test") listening on addr (e.g. "127.0.0.1:53535"),
// and on the same port of every host in also: addresses of this machine that other machines reach
// it on, such as a tailnet address.
func New(addr, tld string, also ...string) *Server {
	return &Server{tld: strings.Trim(strings.ToLower(tld), "."), addr: addr, also: also, watched: map[string]bool{}}
}

// Start binds UDP and TCP on the same port of every address and serves in the background.
func (s *Server) Start() error {
	type listener struct {
		pc net.PacketConn
		ln net.Listener
	}
	pc, ln, err := listen(s.addr)
	if err != nil {
		return err
	}
	listeners := []listener{{pc, ln}}
	_, port, _ := net.SplitHostPort(pc.LocalAddr().String())
	for _, host := range s.also {
		pc, ln, err := listen(net.JoinHostPort(host, port))
		if err != nil {
			for _, l := range listeners {
				_, _ = l.pc.Close(), l.ln.Close()
			}
			return err
		}
		listeners = append(listeners, listener{pc, ln})
	}

	s.addr = pc.LocalAddr().String()
	for _, l := range listeners {
		s.serve(l.pc, l.ln)
	}
	return nil
}

func listen(addr string) (net.PacketConn, net.Listener, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dns udp listen: %w", err)
	}

	// Reuse the port UDP actually got, so ":0" works in tests.
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		_ = pc.Close()
		return nil, nil, fmt.Errorf("dns tcp listen: %w", err)
	}
	return pc, ln, nil
}

func (s *Server) serve(pc net.PacketConn, ln net.Listener) {
	udp := &mdns.Server{PacketConn: pc, Handler: mdns.HandlerFunc(s.handle)}
	tcp := &mdns.Server{Listener: ln, Handler: mdns.HandlerFunc(s.handle)}
	s.servers = append(s.servers, udp, tcp)
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
}

// Addr is the bound address (resolved after Start).
func (s *Server) Addr() string {
	return s.addr
}

// TLD is the domain this server answers for.
func (s *Server) TLD() string {
	return s.tld
}

// Also is the further hosts the server listens on (see New).
func (s *Server) Also() []string {
	return s.also
}

// Watch starts recording queries for name and returns a function reporting whether one arrived.
// The daemon uses it to check that the system resolver really sends the TLD here.
func (s *Server) Watch(name string) (seen func() bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	s.mu.Lock()
	s.watched[name] = false
	s.mu.Unlock()

	return func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		got := s.watched[name]
		delete(s.watched, name)
		return got
	}
}

// Shutdown stops every listener.
func (s *Server) Shutdown() error {
	var firstErr error
	for _, srv := range s.servers {
		if err := srv.Shutdown(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// handle answers A queries with the address they reached (see answer) and everything else (AAAA
// included) with no records. Sites listen on 127.0.0.1 only (on Windows, and on macOS with
// unprivileged ports), so answering AAAA with ::1 makes clients try IPv6 first: PHP's streams then
// wait ~2 s for each connection.
func (s *Server) handle(w mdns.ResponseWriter, req *mdns.Msg) {
	resp := new(mdns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true

	for _, q := range req.Question {
		if !s.owns(q.Name) {
			resp.Rcode = mdns.RcodeRefused
			continue
		}
		s.saw(q.Name)

		if q.Qtype == mdns.TypeA {
			hdr := mdns.RR_Header{Name: q.Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: ttl}
			resp.Answer = append(resp.Answer, &mdns.A{Hdr: hdr, A: answer(w.LocalAddr())})
		}
	}

	_ = w.WriteMsg(resp)
}

// answer is what a query that reached local is answered with: the address it reached, so another
// machine querying a tailnet address gets that address, and loopback otherwise.
func answer(local net.Addr) net.IP {
	var ip net.IP
	switch a := local.(type) {
	case *net.UDPAddr:
		ip = a.IP
	case *net.TCPAddr:
		ip = a.IP
	}
	if ip4 := ip.To4(); ip4 != nil && !ip4.IsUnspecified() {
		return ip4
	}
	return net.IPv4(127, 0, 0, 1)
}

func (s *Server) saw(name string) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	s.mu.Lock()
	if _, ok := s.watched[name]; ok {
		s.watched[name] = true
	}
	s.mu.Unlock()
}

func (s *Server) owns(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")

	return name == s.tld || strings.HasSuffix(name, "."+s.tld)
}
