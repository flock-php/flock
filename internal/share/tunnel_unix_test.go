//go:build !windows

package share

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCloudflared is a cloudflared that prints lines to stderr, then waits to be stopped.
func fakeCloudflared(t *testing.T, lines ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cloudflared")
	script := "#!/bin/sh\ncat >&2 <<'EOF'\n" + strings.Join(lines, "\n") + "\nEOF\nexec sleep 60\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func runTunnel(t *testing.T, bin string) Status {
	t.Helper()
	old := connectTimeout
	connectTimeout = time.Second
	t.Cleanup(func() { connectTimeout = old })

	root := t.TempDir()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{root: root, shares: map[string]*share{}}
	s := &share{Status: Status{Site: "app", State: Starting, Log: LogName("app")}}
	m.shares["app"] = s
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.tunnel(ctx, s, bin, 1, io.Discard)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := m.List()[0]
		if st.State != Starting || time.Now().After(deadline) {
			cancel()
			<-done
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const quickTunnelURL = "INF |  https://quiet-river-lamp.trycloudflare.com                                 |"

func TestTunnelRunsOnceConnected(t *testing.T) {
	st := runTunnel(t, fakeCloudflared(t, quickTunnelURL,
		"INF Registered tunnel connection connIndex=0 connection=df65610b event=0 ip=198.41.192.27 location=prg03 protocol=quic"))
	if st.State != Running || st.URL != "https://quiet-river-lamp.trycloudflare.com" {
		t.Fatalf("got %+v", st)
	}
}

func TestTunnelFailsWhenTheNetworkBlocksIt(t *testing.T) {
	st := runTunnel(t, fakeCloudflared(t, quickTunnelURL,
		"INF precheck complete hard_fail=true run_id=80859060-efed-470e-97cb-1268b3eedf8c",
		`ERR Failed to dial a quic connection error="failed to dial to edge with quic: timeout: no recent network activity" connIndex=0`))
	if st.State != Failed || st.URL != "" || !strings.Contains(st.Error, "port 7844") {
		t.Fatalf("got %+v", st)
	}
}

func TestTunnelFailsWhenItNeverConnects(t *testing.T) {
	st := runTunnel(t, fakeCloudflared(t, quickTunnelURL, "INF precheck complete hard_fail=false"))
	if st.State != Failed || strings.Contains(st.Error, "7844") || !strings.Contains(st.Error, "couldn't connect") {
		t.Fatalf("got %+v", st)
	}
}
