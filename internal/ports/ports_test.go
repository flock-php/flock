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
