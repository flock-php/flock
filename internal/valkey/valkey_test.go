package valkey

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"flock/internal/supervisor"
)

// The build output from build/valkey/build.sh, if present.
const buildDir = "../../build/valkey/out/9.1.2"

func TestInstallAndServeRedisProtocol(t *testing.T) {
	if _, err := os.Stat(buildDir); err != nil {
		t.Skip("no local Valkey build; run build/valkey/build.sh")
	}

	root := t.TempDir()
	version, err := InstallFrom(root, buildDir)
	if err != nil || version != "9.1.2" {
		t.Fatalf("install: %q %v", version, err)
	}
	if versions, _ := Installed(root); len(versions) != 1 {
		t.Fatalf("installed: %v", versions)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	spec, err := Spec(root, version, port)
	if err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New()
	sup.Ensure(spec)
	defer sup.StopAll()

	var conn net.Conn
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if conn, err = net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			break
		}
	}
	if conn == nil {
		t.Fatalf("valkey did not start: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("PING\r\n")); err != nil {
		t.Fatal(err)
	}
	reply, _ := bufio.NewReader(conn).ReadString('\n')
	if reply != "+PONG\r\n" {
		t.Fatalf("got %q", reply)
	}
}
