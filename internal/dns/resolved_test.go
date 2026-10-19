package dns

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvedProblem(t *testing.T) {
	oldUnit, oldRuntime, oldNM, oldUp, oldManages := UnitDir, ResolvedRuntimeDir, NMConfDir, linkUp, nmManages
	UnitDir, ResolvedRuntimeDir, NMConfDir = t.TempDir(), filepath.Join(t.TempDir(), "missing"), t.TempDir()
	up, managed := true, false
	linkUp = func() bool { return up }
	nmManages = func() bool { return managed }
	t.Cleanup(func() {
		UnitDir, ResolvedRuntimeDir, NMConfDir, linkUp, nmManages = oldUnit, oldRuntime, oldNM, oldUp, oldManages
	})

	// Without Flock's unit the link isn't how the TLD resolves here.
	up = false
	if p := LinkProblem("test"); p != "" {
		t.Fatalf("no unit: %q", p)
	}
	up = true

	if p := ResolvedProblem("test", 53535); !strings.Contains(p, "systemd-resolved isn't running") || !strings.Contains(p, "port 53535") {
		t.Fatalf("without systemd-resolved: %q", p)
	}

	ResolvedRuntimeDir = t.TempDir()
	if p := ResolvedProblem("test", 53535); !strings.Contains(p, "missing") || !strings.Contains(p, "flock-php install") {
		t.Fatalf("missing unit: %q", p)
	}

	if err := os.WriteFile(ResolvedFile(), []byte(ResolvedContent("test", 53535)), 0o644); err != nil {
		t.Fatal(err)
	}
	// Installs from before the NetworkManager drop-in run the setup again.
	if p := ResolvedProblem("test", 53535); !strings.Contains(p, NMFile()) || !strings.Contains(p, "flock-php install") {
		t.Fatalf("missing NetworkManager drop-in: %q", p)
	}
	if err := os.WriteFile(NMFile(), []byte(NMContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := ResolvedProblem("test", 53535); p != "" {
		t.Fatalf("current unit: %q", p)
	}
	// Something took the link down or flushed its address after setup.
	up = false
	if p := ResolvedProblem("test", 53535); !strings.Contains(p, "flock0 is down") || !strings.Contains(p, "systemctl restart flock-dns.service") || p != LinkProblem("test") {
		t.Fatalf("link without its address: %q", p)
	}
	up = true
	// A drop-in read after Flock's replaced its unmanaged-devices list.
	managed = true
	if p := LinkProblem("test"); !strings.Contains(p, "NetworkManager manages flock0") || !strings.Contains(p, "zz-flock.conf") {
		t.Fatalf("managed by NetworkManager: %q", p)
	}
	managed = false
	if p := ResolvedProblem("test", 5300); !strings.Contains(p, "port 5300") || !strings.Contains(p, "flock-php install") {
		t.Fatalf("other port: %q", p)
	}
	if got := ResolvedFile(); got != filepath.Join(UnitDir, "flock-dns.service") {
		t.Fatalf("file: %q", got)
	}
}

// The TLD goes to Flock through a link of its own, never through resolved's global servers.
func TestResolvedContentRoutesTheTLDThroughFlocksLink(t *testing.T) {
	unit := ResolvedContent("test", 53535)
	for _, want := range []string{
		"ExecStart=ip link add flock0 type dummy",
		"ExecStart=ip address add 198.18.53.53/32 dev flock0",
		`resolvectl dns flock0 127.0.0.1:53535 && resolvectl domain flock0 "~test"`,
		"ExecStop=ip link delete flock0",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("missing %q in:\n%s", want, unit)
		}
	}
	// A literal % would be a systemd specifier.
	if strings.Contains(unit, "%") || strings.Contains(unit, "[Resolve]") {
		t.Errorf("unit:\n%s", unit)
	}
	if !strings.Contains(NMContent, "[keyfile]\nunmanaged-devices+=interface-name:flock0\n") {
		t.Errorf("NetworkManager drop-in:\n%s", NMContent)
	}
	if got := LegacyDropIn("test"); got != filepath.Join(ResolvedDir, "flock-test.conf") {
		t.Errorf("legacy drop-in: %q", got)
	}
}
