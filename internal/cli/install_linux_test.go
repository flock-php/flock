package cli

import (
	"encoding/binary"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// Paths with spaces, quotes, % and $ reach the daemon as they are.
func TestUnitFileQuotesPaths(t *testing.T) {
	unit := unitFile(`/home/o"b/50% $x/Flock`, `/home/o"b/50% $x/Flock/bin/flock`, "/l/daemon.log")
	want := `ExecStart="/home/o\"b/50%% $$x/Flock/bin/flock" daemon --log-file "/l/daemon.log"`
	if !strings.Contains(unit, want+"\n") {
		t.Fatalf("unit:\n%s\nwant the line\n%s", unit, want)
	}
	if !strings.Contains(unit, `"FLOCK_HOME=/home/o\"b/50%% $x/Flock"`) {
		t.Fatalf("unit has no FLOCK_HOME:\n%s", unit)
	}
}

// The daemon finds the same commands as a terminal: Flock's shims, then global npm packages.
func TestServicePathMatchesTheShellPath(t *testing.T) {
	got := strings.Split(servicePath("/f"), ":")
	if !slices.Equal(got[:2], []string{"/f/bin", "/f/node-global/current/bin"}) || !slices.Contains(got, "/usr/bin") {
		t.Fatalf("PATH %v", got)
	}
}

// A detached daemon that has exited counts as gone even when nobody reaps it.
func TestProcessGoneTreatsZombiesAsGone(t *testing.T) {
	if processGone(os.Getpid()) {
		t.Fatal("this process is gone")
	}
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Wait()
	for deadline := time.Now().Add(5 * time.Second); !processGone(cmd.Process.Pid); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("an exited, unreaped process isn't gone")
		}
	}
}

func TestBindCapability(t *testing.T) {
	attr := func(flags, permitted uint32) []byte {
		b := make([]byte, 20)
		binary.LittleEndian.PutUint32(b[0:4], 0x02000000|flags) // VFS_CAP_REVISION_2
		binary.LittleEndian.PutUint32(b[4:8], permitted)
		return b
	}
	for _, c := range []struct {
		data []byte
		want bool
	}{
		{attr(1, 1<<10), true},            // cap_net_bind_service=+ep
		{attr(0, 1<<10), false},           // +p only: not raised at exec
		{attr(1, 1<<12), false},           // another capability
		{attr(1, 1<<10|1<<12)[:4], false}, // truncated
		{nil, false},
	} {
		if got := bindCapability(c.data); got != c.want {
			t.Errorf("bindCapability(%x) = %v, want %v", c.data, got, c.want)
		}
	}
}
