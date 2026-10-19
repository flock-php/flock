package ports

import (
	"net"
	"os"
	"testing"
)

// Without lsof, /proc still names the process listening on a port.
func TestProcListenersFindsThisProcess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	found := procListeners(port)
	if len(found) != 1 || found[0].PID != os.Getpid() || found[0].Name == "" {
		t.Fatalf("procListeners(%d) = %+v, want this process (pid %d)", port, found, os.Getpid())
	}
	if found := procListeners(port + 1); len(found) != 0 && found[0].PID == os.Getpid() {
		t.Fatalf("procListeners on another port found this process: %+v", found)
	}
}
