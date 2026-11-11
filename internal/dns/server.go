// Package dns answers every query under the active suffixes (e.g. *.test, *.loc) with loopback.
//
// On macOS it is wired up via /etc/resolver/<suffix> files with a non-privileged port, so it never
// needs root. On Windows it listens on port 53 (Windows can't send queries to another port) and an
// NRPT rule per suffix (see nrpt_windows.go) sends them to it; the hosts file stays as the fallback.
package dns

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"

	mdns "github.com/miekg/dns"
)

const ttl = 60

// Server is a tiny authoritative DNS server for a set of local suffixes.
type Server struct {
	tlds []string
	addr string
	udp  *mdns.Server
	tcp  *mdns.Server

	mu      sync.Mutex
	watched map[string]bool // name → queried since Watch
}

// New creates a server answering names under any of tlds (without dots, e.g. "test") listening
// on addr (e.g. "127.0.0.1:53535"). The set is normalized: lower-case, deduplicated, sorted.
func New(addr string, tlds []string) *Server {
	return &Server{tlds: normalizeSuffixes(tlds), addr: addr, watched: map[string]bool{}}
}

// normalizeSuffixes turns a suffix list into the canonical set: trimmed of dots, lower-case,
// deduplicated, sorted. Provisions that compare sets (the daemon, the resolver files) rely on it.
func normalizeSuffixes(tlds []string) []string {
	set := make([]string, 0, len(tlds))
	for _, tld := range tlds {
		tld = strings.Trim(strings.ToLower(tld), ".")
		if tld == "" || slices.Contains(set, tld) {
			continue
		}
		set = append(set, tld)
	}
	slices.Sort(set)
	return set
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

// TLDs lists the suffixes this server answers for, sorted.
func (s *Server) TLDs() []string {
	return slices.Clone(s.tlds)
}

// Watch starts recording queries for name and returns a function reporting whether one arrived.
// The daemon uses it to check that the system resolver really sends these names here.
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

// handle answers A queries with 127.0.0.1 and everything else (AAAA included) with no records.
// Sites listen on 127.0.0.1 only (on Windows, and on macOS with unprivileged ports), so answering
// AAAA with ::1 makes clients try IPv6 first: PHP's streams then wait ~2 s for each connection.
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
			resp.Answer = append(resp.Answer, &mdns.A{Hdr: hdr, A: net.IPv4(127, 0, 0, 1)})
		}
	}

	_ = w.WriteMsg(resp)
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

	for _, tld := range s.tlds {
		if name == tld || strings.HasSuffix(name, "."+tld) {
			return true
		}
	}
	return false
}
