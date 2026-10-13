package dumps

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flock/internal/supervisor"
)

func TestExtractIsIdempotent(t *testing.T) {
	root := t.TempDir()
	dir, err := Extract(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vendor", "autoload.php")); err != nil {
		t.Fatal("vendor not extracted")
	}

	marker := filepath.Join(dir, "server.php")
	before, _ := os.Stat(marker)
	if _, err := Extract(root); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(marker)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged server must not be rewritten")
	}
}

func TestListTrimsAndOrdersNewestFirst(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(File(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < MaxEntries+5; i++ {
		fmt.Fprintf(&b, `{"id":"%d","time":"2026-10-13T12:00:00.000+00:00","text":"t%d","html":""}`+"\n", i, i)
	}
	if err := os.WriteFile(File(root), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := List(root)
	if err != nil || len(entries) != MaxEntries || entries[0].ID != fmt.Sprint(MaxEntries+4) {
		t.Fatalf("got %d entries, first %+v, err %v", len(entries), entries[0], err)
	}
	if Count(root) != MaxEntries {
		t.Fatalf("count %d", Count(root))
	}

	if err := Clear(root); err != nil || Count(root) != 0 {
		t.Fatal("clear failed")
	}
}

// TestServerReceivesDumps needs a PHP binary; it uses the one from PATH or FLOCK_TEST_PHP.
func TestServerReceivesDumps(t *testing.T) {
	php := os.Getenv("FLOCK_TEST_PHP")
	if php == "" {
		var err error
		if php, err = exec.LookPath("php"); err != nil {
			t.Skip("no php available")
		}
	}
	if err := exec.Command(php, "-n", "-r", "exit(PHP_VERSION_ID >= 80400 ? 0 : 1);").Run(); err != nil {
		t.Skipf("%s is not a working PHP >= 8.4 (set FLOCK_TEST_PHP)", php)
	}

	root := t.TempDir()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	spec, err := Spec(root, php, port)
	if err != nil {
		t.Fatal(err)
	}
	sup := supervisor.New()
	sup.Ensure(spec)
	defer sup.StopAll()

	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dump server did not start")
		}
	}

	// A client using the bundled VarDumper, exactly like an app with VAR_DUMPER_FORMAT=server.
	dir := filepath.Join(root, "tools", "dump-server")
	client := exec.Command(php, "-n", "-r", `require $argv[1]."/vendor/autoload.php"; dump(["flock" => true]);`, dir)
	client.Env = append(os.Environ(), Env(port)...)
	if out, err := client.CombinedOutput(); err != nil {
		t.Fatalf("client: %v %s", err, out)
	}

	for deadline := time.Now().Add(5 * time.Second); Count(root) == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("dump not received")
		}
	}
	entries, _ := List(root)
	if !strings.Contains(entries[0].Text, `"flock" => true`) || entries[0].Source == nil || !strings.Contains(entries[0].HTML, "sf-dump") || entries[0].Group == "" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
	if strings.Contains(entries[0].HTML, "<style") {
		t.Fatal("entries must not repeat the dumper header")
	}
	if header, err := Header(root); err != nil || !strings.Contains(header, "Sfdump") {
		t.Fatalf("header missing: %v", err)
	}
}
