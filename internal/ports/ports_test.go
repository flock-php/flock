package ports

import (
	"net"
	"runtime"
	"strings"
	"testing"
)

func TestDetectsBusyPortsAndTheirOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if Free("127.0.0.1", port) {
		t.Fatal("port should be busy")
	}
	if runtime.GOOS == "darwin" && !strings.Contains(Describe(port), "pid") {
		t.Fatalf("owner not found: %s", Describe(port))
	}

	ln.Close()
	if !Free("127.0.0.1", port) {
		t.Fatal("port should be free after close")
	}
}

func TestListenerPIDParsesNetstat(t *testing.T) {
	out := `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1012
  TCP    0.0.0.0:8080           0.0.0.0:0              LISTENING       77
  TCP    127.0.0.1:80           0.0.0.0:0              LISTENING       4
  TCP    127.0.0.1:52000        127.0.0.1:80           ESTABLISHED     9000
  TCP    [::]:443               [::]:0                 LISTENING       812
`
	for port, want := range map[int]string{80: "4", 443: "812", 8080: "77", 8: ""} {
		if got := listenerPID(out, port); got != want {
			t.Errorf("port %d: got %q, want %q", port, got, want)
		}
	}
}
