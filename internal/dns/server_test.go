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

func TestAnswersAAAAWithIPv6Loopback(t *testing.T) {
	resp := query(t, startServer(t), "udp", "watchtower.test", mdns.TypeAAAA)

	if len(resp.Answer) != 1 || !resp.Answer[0].(*mdns.AAAA).AAAA.Equal(net.IPv6loopback) {
		t.Fatalf("want ::1, got %v", resp.Answer)
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
