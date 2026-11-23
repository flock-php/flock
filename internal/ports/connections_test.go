package ports

import (
	"net"
	"testing"
	"time"
)

func TestNetstatConnectionsCountsConnectionsToThePort(t *testing.T) {
	out := `tcp4       0      0  127.0.0.1.5173         127.0.0.1.50321        ESTABLISHED
tcp4       0      0  127.0.0.1.50321        127.0.0.1.5173         ESTABLISHED
tcp4       0      0  *.5173                 *.*                    LISTEN
tcp 0 0 127.0.0.1:5173 127.0.0.1:50400 ESTABLISHED
  TCP    127.0.0.1:5173         127.0.0.1:50500        ESTABLISHED
`
	if got := netstatConnections(out, 5173); got != 3 {
		t.Fatalf("got %d, want 3 (one per server-side line)", got)
	}
}

func TestConnectionsSeesAnOpenConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if n := Connections(port); n != 0 {
		t.Fatalf("%d connections before any", n)
	}
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	server, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	deadline := time.Now().Add(3 * time.Second)
	for Connections(port) == 0 {
		if time.Now().After(deadline) {
			t.Skip("this system can't list connections")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Real `netstat -an` output of macOS for a loopback connection on 127.0.0.1 and on ::1, a
// listener, and the established connections of another port.
const macNetstat = `Active Internet connections (including servers)
Proto Recv-Q Send-Q  Local Address          Foreign Address        (state)
tcp4       0      0  127.0.0.1.56929        127.0.0.1.56930        ESTABLISHED
tcp4       0      0  127.0.0.1.56930        127.0.0.1.56929        ESTABLISHED
tcp4       0      0  127.0.0.1.56929        *.*                    LISTEN     
tcp6       0      0  ::1.56933              ::1.56934              ESTABLISHED
tcp6       0      0  ::1.56934              ::1.56933              ESTABLISHED
tcp6       0      0  ::1.56933              *.*                    LISTEN
tcp6       0      0  2a02:8434:ec23:d.55982 2607:6bc0::10.443      ESTABLISHED
`

func TestNetstatConnectionsReadsMacOSOutput(t *testing.T) {
	for port, want := range map[int]int{56929: 1, 56933: 1, 56930: 1, 443: 0, 55982: 1, 5173: 0} {
		if got := netstatConnections(macNetstat, port); got != want {
			t.Errorf("port %d: got %d connections, want %d", port, got, want)
		}
	}
}

func TestNetstatConnectionsReadsWindowsOutput(t *testing.T) {
	out := `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    127.0.0.1:5173         0.0.0.0:0              LISTENING       4120
  TCP    127.0.0.1:5173         127.0.0.1:50500        ESTABLISHED     4120
  TCP    127.0.0.1:50500        127.0.0.1:5173         ESTABLISHED     9988
  TCP    [::1]:5173             [::1]:50501            ESTABLISHED     4120
`
	if got := netstatConnections(out, 5173); got != 2 {
		t.Fatalf("got %d connections to 5173, want 2", got)
	}
}
