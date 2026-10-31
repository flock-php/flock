package ports

import (
	"net"
	"os"
	"runtime"
	"slices"
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
	if runtime.GOOS == "darwin" {
		holders := Holders(port)
		if len(holders) != 1 || holders[0].PID != os.Getpid() || holders[0].Path == "" {
			t.Fatalf("holders = %+v, want this test (pid %d)", holders, os.Getpid())
		}
		if !strings.Contains(Describe(port), "pid") {
			t.Fatalf("owner not found: %s", Describe(port))
		}
		// The test binary lives in a temp dir: Flock's own when that is the root.
		if len(Foreign(port, "/nonexistent")) != 1 {
			t.Fatal("the listener should be foreign to another root")
		}
		dir := holders[0].Path[:strings.LastIndex(holders[0].Path, "/")]
		if f := Foreign(port, dir); len(f) != 0 {
			t.Fatalf("a program under the root is Flock's, got %+v", f)
		}
	}

	ln.Close()
	if !Free("127.0.0.1", port) {
		t.Fatal("port should be free after close")
	}
	if f := Foreign(port, ""); len(f) != 0 {
		t.Fatalf("free port has holders %+v", f)
	}
}

func TestListenerPIDsParsesNetstat(t *testing.T) {
	out := `
Active Connections

  Proto  Local Address          Foreign Address        State           PID
  TCP    0.0.0.0:135            0.0.0.0:0              LISTENING       1012
  TCP    0.0.0.0:8080           0.0.0.0:0              LISTENING       77
  TCP    127.0.0.1:80           0.0.0.0:0              LISTENING       4
  TCP    127.0.0.1:52000        127.0.0.1:80           ESTABLISHED     9000
  TCP    [::]:443               [::]:0                 LISTENING       812
  TCP    0.0.0.0:443            0.0.0.0:0              LISTENING       812
  TCP    127.0.0.1:443          0.0.0.0:0              LISTENING       90
`
	for port, want := range map[int][]int{80: {4}, 443: {812, 90}, 8080: {77}, 8: nil} {
		if got := listenerPIDs(out, port); !slices.Equal(got, want) {
			t.Errorf("port %d: got %v, want %v", port, got, want)
		}
	}
}

func TestAppFromPath(t *testing.T) {
	for _, c := range []struct{ path, home, app, brew string }{
		{"/Applications/Herd.app/Contents/Resources/nginx-arm64", "/Users/me", "Herd", ""},
		{"/Applications/Docker.app/Contents/Resources/bin/com.docker.backend", "/Users/me", "Docker", ""},
		{"/Applications/Postgres.app/Contents/Versions/17/bin/postgres", "/Users/me", "Postgres", ""},
		{"/Users/me/Library/Application Support/Herd/bin/tools/herd-devtools-helper", "/Users/me", "Herd", ""},
		{"/opt/homebrew/Cellar/nginx/1.27.4/bin/nginx", "/Users/me", "Homebrew", "nginx"},
		{"/usr/local/opt/postgresql@17/bin/postgres", "/Users/me", "Homebrew", "postgresql@17"},
		{"/Users/me/.config/herd/bin/nginx", "/Users/me", "Herd", ""},
		{"/Users/me/.lando/bin/lando", "/Users/me", "Lando", ""},
		{`C:\Program Files\Herd\resources\app\nginx.exe`, `C:\Users\me`, "Herd", ""},
		{`C:\Users\me\.config\herd\bin\nginx\nginx.exe`, `C:\Users\me`, "Herd", ""},
		{`C:\Users\me\AppData\Local\Programs\Laragon\bin\nginx.exe`, `C:\Users\me`, "Laragon", ""},
		{`C:\ProgramData\MySQL\bin\mysqld.exe`, `C:\Users\me`, "MySQL", ""},
		{`C:\Program Files\Common Files\thing\svc.exe`, `C:\Users\me`, "", ""},
		{"/usr/sbin/httpd", "/Users/me", "", ""},
		{"/Users/me/go/bin/server", "/Users/me", "", ""},
		{"/Users/me/.vscode", "/Users/me", "", ""},
	} {
		app, brew := appFromPath(c.path, c.home)
		if app != c.app || brew != c.brew {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.path, app, brew, c.app, c.brew)
		}
	}
}

func TestHolderTexts(t *testing.T) {
	for _, c := range []struct {
		h          Holder
		name, hint string
	}{
		{Holder{Port: 80, PID: 812, Process: "nginx-arm64", App: "Herd"}, "Herd (nginx-arm64, pid 812)", "quit Herd"},
		{Holder{Port: 5432, PID: 9, Process: "postgres", App: "Homebrew", Brew: "postgresql@17"}, "Homebrew (postgres, pid 9)", "run `brew services stop postgresql@17`"},
		{Holder{Port: 80, PID: 812, Process: "httpd"}, "httpd (pid 812)", "quit httpd (pid 812)"},
		{Holder{Port: 80}, "another program", ""},
	} {
		if got := c.h.String(); got != c.name {
			t.Errorf("String() = %q, want %q", got, c.name)
		}
		if got := c.h.Hint(); got != c.hint {
			t.Errorf("Hint() = %q, want %q", got, c.hint)
		}
	}
}

func TestOwnProcesses(t *testing.T) {
	root := "/Users/me/Library/Application Support/Flock"
	for _, c := range []struct {
		h    Holder
		want bool
	}{
		{Holder{Path: root + "/bin/flock"}, true},
		{Holder{Path: root + "/php/8.5/sbin/php-fpm"}, true},
		{Holder{Process: "flock"}, true},
		{Holder{Path: `C:\Program Files\Flock\flockd.exe`}, true},
		{Holder{Path: "/Applications/Herd.app/Contents/Resources/nginx-arm64"}, false},
		{Holder{Path: root + "-old/bin/php"}, false},
	} {
		if got := Own(c.h, root); got != c.want {
			t.Errorf("Own(%+v) = %v, want %v", c.h, got, c.want)
		}
	}
}
