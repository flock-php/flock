package cli

import (
	"errors"
	"fmt"
	"testing"
)

// A declined prompt exits with exitDeclined, even wrapped; any other error with 1.
func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"declined", errDeclined, exitDeclined},
		{"declined, wrapped", fmt.Errorf("privileged setup: %w", errDeclined), exitDeclined},
		{"other error", errors.New("something went wrong"), 1},
		{"same text, not errDeclined", errors.New("administrator permission was declined"), 1},
	} {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("%s: ExitCode(%q) = %d, want %d", tc.name, tc.err, got, tc.want)
		}
	}
}
