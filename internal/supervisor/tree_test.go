package supervisor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The test binary plays a launcher (like npm) and the child it starts (like the dev server that
// holds the port), chosen by FLOCK_TEST_TREE.
func TestMain(m *testing.M) {
	switch os.Getenv("FLOCK_TEST_TREE") {
	case "":
		os.Exit(m.Run())
	case "child":
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			os.Exit(2)
		}
		f, err := os.OpenFile(os.Getenv("FLOCK_TEST_TREE_FILE"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(f, ln.Addr().String())
		f.Close()
		for { // accept, so the tests' dials never fill the backlog
			conn, err := ln.Accept()
			if err != nil {
				os.Exit(2)
			}
			conn.Close()
		}
	default: // "launcher-wait" or "launcher-exit"
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "FLOCK_TEST_TREE=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if os.Getenv("FLOCK_TEST_TREE") == "launcher-exit" {
			time.Sleep(500 * time.Millisecond) // the child is up; the launcher "crashes"
			os.Exit(1)
		}
		time.Sleep(time.Hour)
	}
}

// treeSpec runs the test binary as a launcher whose child writes the address it holds to a file.
func treeSpec(t *testing.T, mode string) (Spec, string) {
	t.Helper()
	dir := t.TempDir()
	addrs := filepath.Join(dir, "addrs")
	return Spec{
		Name: "tree", Path: os.Args[0], Tree: true, LogFile: filepath.Join(dir, "tree.log"),
		Env: []string{"FLOCK_TEST_TREE=" + mode, "FLOCK_TEST_TREE_FILE=" + addrs},
	}, addrs
}

func childAddrs(path string) []string {
	data, _ := os.ReadFile(path)
	return strings.Fields(string(data))
}

func listening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestTreeStopsTheLaunchersChildren(t *testing.T) {
	spec, addrs := treeSpec(t, "launcher-wait")
	s := New()
	s.Ensure(spec)
	waitUntil(t, "the child listens", func() bool {
		a := childAddrs(addrs)
		return len(a) == 1 && listening(a[0])
	})

	s.StopAll()
	if addr := childAddrs(addrs)[0]; listening(addr) {
		t.Fatalf("the child still holds %s after the launcher was stopped", addr)
	}
}

func TestTreeStopsWhatsLeftWhenTheLauncherExits(t *testing.T) {
	spec, addrs := treeSpec(t, "launcher-exit")
	s := New()
	defer s.StopAll()
	s.Ensure(spec)

	// The launcher exits, leaving its child; the restart starts a second one.
	waitUntil(t, "the launcher is restarted", func() bool { return len(childAddrs(addrs)) >= 2 })
	waitUntil(t, "the first child is stopped", func() bool { return !listening(childAddrs(addrs)[0]) })
}
