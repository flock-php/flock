package valkey

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// fakeServer answers the first command it reads with reply ("" closes the connection, as a server
// does when it shuts down) and reports the command.
func fakeServer(t *testing.T, reply string) (int, <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		var cmd strings.Builder
		for range 3 {
			line, _ := r.ReadString('\n')
			cmd.WriteString(line)
		}
		got <- cmd.String()
		if reply != "" {
			_, _ = conn.Write([]byte(reply))
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, got
}

func TestShutdownSendsTheCommandAndTreatsAClosedConnectionAsSuccess(t *testing.T) {
	port, got := fakeServer(t, "")
	if err := Shutdown(port); err != nil {
		t.Fatal(err)
	}
	if cmd := <-got; cmd != "*1\r\n$8\r\nSHUTDOWN\r\n" {
		t.Fatalf("sent %q", cmd)
	}
}

func TestShutdownReportsAnErrorReply(t *testing.T) {
	port, _ := fakeServer(t, "-ERR Errors trying to SHUTDOWN. Check logs.\r\n")
	if err := Shutdown(port); err == nil || !strings.Contains(err.Error(), "Errors trying to SHUTDOWN") {
		t.Fatalf("want the server's error, got %v", err)
	}
}

func TestShutdownFailsWithoutAServer(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if err := Shutdown(port); err == nil {
		t.Fatal("want a dial error")
	}
}
