package cli

import (
	"os"
	"testing"

	"flock/internal/config"
)

func TestProxyCommandFlags(t *testing.T) {
	cwd, _ := os.Getwd()
	other := t.TempDir()
	run := func(p config.Proxy, args ...string) (config.Proxy, error) {
		t.Helper()
		cmd := newProxyCommand()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		get := func(name string) string { v, _ := cmd.Flags().GetString(name); return v }
		err := applyCommandFlags(cmd, &p, get("command"), get("dir"), get("stop-command"))
		return p, err
	}

	// A new command runs in the current folder, or --dir.
	if p, err := run(config.Proxy{}, "--command", " npm run dev "); err != nil || p.Command != "npm run dev" || p.Dir != cwd {
		t.Fatalf("command: %+v %v", p, err)
	}
	if p, err := run(config.Proxy{}, "--command", "npm run dev", "--dir", other); err != nil || p.Dir != other {
		t.Fatalf("--dir: %+v %v", p, err)
	}
	if _, err := run(config.Proxy{}, "--command", "x", "--dir", other+"/missing"); err == nil {
		t.Fatal("a missing folder was taken")
	}

	// Left out, the command stays; an empty one is removed with its folder and stop command.
	existing := config.Proxy{Command: "docker compose up -d", Dir: other, StopCommand: "docker compose stop"}
	if p, _ := run(existing); p != existing {
		t.Fatalf("kept: %+v", p)
	}
	if p, _ := run(existing, "--command", ""); p.Command != "" || p.Dir != "" || p.StopCommand != "" {
		t.Fatalf("removed: %+v", p)
	}
}

func TestProxyStartIsASubcommand(t *testing.T) {
	cmd, args, err := newProxyCommand().Find([]string{"start", "admin"})
	if err != nil || cmd.Name() != "start" || len(args) != 1 || args[0] != "admin" {
		t.Fatalf("got %s %v %v", cmd.Name(), args, err)
	}
	// A name and an upstream still add a proxy.
	if cmd, _, err := newProxyCommand().Find([]string{"admin", "8000"}); err != nil || cmd.Name() != "proxy" {
		t.Fatalf("got %s %v", cmd.Name(), err)
	}
}
