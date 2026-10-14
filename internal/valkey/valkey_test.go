package valkey

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"flock/internal/supervisor"
)

// The build output from build/valkey/build.sh, if present.
const buildDir = "../../build/valkey/out/9.1.2"

// realServer returns a Valkey/Redis server binary to test against: FLOCK_VALKEY_SERVER, or the
// local build.
func realServer(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("FLOCK_VALKEY_SERVER"); path != "" {
		return path
	}
	path := filepath.Join(buildDir, exe("valkey-server"))
	if _, err := os.Stat(path); err != nil {
		t.Skip("no local Valkey build; run build/valkey/build.sh or set FLOCK_VALKEY_SERVER")
	}
	return path
}

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

func TestShutdownMakesARealServerSaveAndExit(t *testing.T) {
	server := realServer(t)
	dir := t.TempDir()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cmd := exec.Command(server, "--port", strconv.Itoa(port), "--bind", "127.0.0.1", "--dir", dir, "--save", "3600 1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer cmd.Process.Kill()

	var conn net.Conn
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if conn, err = net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			break
		}
	}
	if conn == nil {
		t.Fatalf("server did not start: %v", err)
	}
	_, _ = conn.Write([]byte("SET k v\r\n"))
	if reply, _ := bufio.NewReader(conn).ReadString('\n'); reply != "+OK\r\n" {
		t.Fatalf("SET: %q", reply)
	}
	conn.Close()

	if err := Shutdown(port); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("server exited with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server still running after SHUTDOWN")
	}
	if _, err := os.Stat(filepath.Join(dir, "dump.rdb")); err != nil {
		t.Fatalf("no snapshot saved: %v", err)
	}
}
