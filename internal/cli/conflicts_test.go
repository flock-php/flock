package cli

import (
	"errors"
	"strings"
	"testing"

	"flock/internal/ports"
)

func TestResolverPort(t *testing.T) {
	for content, want := range map[string]int{
		"nameserver 127.0.0.1\n":                  53,
		"nameserver 127.0.0.1\nport 53535\n":      53535,
		"# Some app\nnameserver ::1\nport 1053\n": 1053,
		"nameserver 10.0.0.1\nport 53\n":          0,
		"domain test\nsearch_order 1\n":           0,
	} {
		if got := resolverPort(content); got != want {
			t.Errorf("%q: got %d, want %d", content, got, want)
		}
	}
}

func TestConflictsErrorNamesWhatToQuit(t *testing.T) {
	herd := ports.Holder{PID: 812, Process: "nginx-arm64", App: "Herd"}
	brew := ports.Holder{PID: 9, Process: "nginx", App: "Homebrew", Brew: "nginx"}
	err := conflictsError([]conflict{
		portConflict(corePort{80, "HTTP"}, herd),
		portConflict(corePort{443, "HTTPS"}, herd),
		portConflict(corePort{2525, "mail"}, brew),
	})
	if !errors.Is(err, errConflicts) {
		t.Fatalf("not errConflicts: %v", err)
	}
	for _, want := range []string{
		"port 80 (HTTP) is used by Herd (nginx-arm64, pid 812) — quit Herd",
		"port 2525 (mail) is used by Homebrew (nginx, pid 9) — run `brew services stop nginx`",
		"Quit Herd and Homebrew's nginx first",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%s", want, err)
		}
	}
}
