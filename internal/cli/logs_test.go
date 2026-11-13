package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestTailFilePrintsTheLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web.log")
	var content strings.Builder
	for i := 1; i <= 5000; i++ {
		content.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, n := range []int{1, 3, 4999, 5000, 9000} {
		var out bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&out)
		cmd.SetContext(context.Background())

		if err := tailFile(cmd, path, n, false); err != nil {
			t.Fatal(err)
		}

		got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		want := min(n, 5000)
		if len(got) != want || got[len(got)-1] != "line 5000" || got[0] != "line "+strconv.Itoa(5000-want+1) {
			t.Fatalf("n=%d: got %d lines, first %q last %q", n, len(got), got[0], got[len(got)-1])
		}
	}
}

func TestLogsFindASitesOrProxysLogByItsName(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, age time.Duration) {
		path := filepath.Join(dir, name+".log")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	write("web", 0)
	write("proxy-admin", 0)
	write("frankenphp-shop", time.Hour) // the site ran on FrankenPHP before
	write("node-shop", 0)               // and is a Node.js app now

	for name, want := range map[string]string{"web": "web", "admin": "proxy-admin", "shop": "node-shop", "proxy-admin": "proxy-admin"} {
		if path, ok := logFor(dir, name); !ok || filepath.Base(path) != want+".log" {
			t.Errorf("%s: %q %v, want %s", name, path, ok, want)
		}
	}
	if _, ok := logFor(dir, "missing"); ok {
		t.Error("found a log that doesn't exist")
	}
}
