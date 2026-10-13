package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
