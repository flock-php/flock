package dns

import (
	"net"
	"testing"

	mdns "github.com/miekg/dns"
)

func startServer(t *testing.T) *Server {
	t.Helper()

	srv := New("127.0.0.1:0", "test")
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown() })

	return srv
}

func query(t *testing.T, srv *Server, net, name string, qtype uint16) *mdns.Msg {
	t.Helper()

	msg := new(mdns.Msg)
	msg.SetQuestion(mdns.Fqdn(name), qtype)

	client := &mdns.Client{Net: net}
	resp, _, err := client.Exchange(msg, srv.Addr())
	if err != nil {
		t.Fatalf("exchange %s over %s: %v", name, net, err)
	}

	return resp
}

func TestResolvesSitesAndSubdomainsToLoopback(t *testing.T) {
	srv := startServer(t)

	for _, name := range []string{"watchtower.test", "api.watchtower.test", "WATCHTOWER.TEST"} {
		for _, proto := range []string{"udp", "tcp"} {
			resp := query(t, srv, proto, name, mdns.TypeA)
			if len(resp.Answer) != 1 {
				t.Fatalf("%s/%s: want 1 answer, got %v", name, proto, resp.Answer)
			}
			if a := resp.Answer[0].(*mdns.A).A; !a.Equal(net.IPv4(127, 0, 0, 1)) {
				t.Fatalf("%s/%s: want 127.0.0.1, got %s", name, proto, a)
			}
		}
	}
}

func TestResolvesDeepSubdomains(t *testing.T) {
	resp := query(t, startServer(t), "udp", "a.b.c.d.watchtower.test", mdns.TypeA)

	if len(resp.Answer) != 1 || !resp.Answer[0].(*mdns.A).A.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("want 127.0.0.1, got %v", resp.Answer)
	}
}

// Sites listen on 127.0.0.1 only: an AAAA answer of ::1 would make clients try IPv6 first and wait.
func TestAnswersAAAAWithNoRecords(t *testing.T) {
	resp := query(t, startServer(t), "udp", "watchtower.test", mdns.TypeAAAA)

	if resp.Rcode != mdns.RcodeSuccess || len(resp.Answer) != 0 {
		t.Fatalf("want NOERROR with no answers, got rcode=%d answers=%v", resp.Rcode, resp.Answer)
	}
}

func TestWatchReportsWhetherANameWasQueried(t *testing.T) {
	srv := startServer(t)

	seen := srv.Watch("flock-check-1.test")
	query(t, srv, "udp", "other.test", mdns.TypeA)
	if seen() {
		t.Fatal("a query for another name must not count")
	}

	seen = srv.Watch("flock-check-2.test")
	query(t, srv, "udp", "FLOCK-CHECK-2.test", mdns.TypeA)
	if !seen() {
		t.Fatal("the watched name was queried")
	}
}

func TestRefusesNamesOutsideTheTLD(t *testing.T) {
	srv := startServer(t)

	for _, name := range []string{"example.com", "nottest", "test.com"} {
		resp := query(t, srv, "udp", name, mdns.TypeA)
		if resp.Rcode != mdns.RcodeRefused || len(resp.Answer) != 0 {
			t.Fatalf("%s: want REFUSED with no answers, got rcode=%d answers=%v", name, resp.Rcode, resp.Answer)
		}
	}
}
