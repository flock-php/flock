package xdebug

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const withBreakpoint = `<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="ChangeListManager" />
  <component name="XDebuggerManager">
    <breakpoint-manager>
      <breakpoints>
        <line-breakpoint enabled="true" type="php">
          <url>file://$PROJECT_DIR$/app/Http/Controllers/HomeController.php</url>
          <line>12</line>
          <option name="timeStamp" value="1" />
        </line-breakpoint>
      </breakpoints>
    </breakpoint-manager>
  </component>
</project>`

func TestHasBreakpoints(t *testing.T) {
	for name, tc := range map[string]struct {
		xml  string
		want bool
	}{
		"php breakpoint": {withBreakpoint, true},
		"disabled":       {strings.Replace(withBreakpoint, `enabled="true"`, `enabled="false"`, 1), false},
		"javascript":     {strings.Replace(withBreakpoint, `type="php"`, `type="javascript"`, 1), false},
		"no breakpoints": {`<project version="4"><component name="XDebuggerManager"><breakpoint-manager /></component></project>`, false},
		// Only the debugger's own component counts.
		"other component": {`<project><component name="Other"><line-breakpoint enabled="true" type="php" /></component></project>`, false},
		"empty":           {``, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := HasBreakpoints(strings.NewReader(tc.xml))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("HasBreakpoints = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDetectorFollowsTheWorkspaceFile(t *testing.T) {
	project := t.TempDir()
	var d Detector
	if d.Has(project) {
		t.Fatal("a project without .idea has breakpoints")
	}

	file := Workspace(project)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(withBreakpoint), 0o644); err != nil {
		t.Fatal(err)
	}
	if !d.Has(project) {
		t.Fatal("breakpoint not detected")
	}

	if err := os.WriteFile(file, []byte(`<project version="4" />`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Some file systems keep coarse modification times; make sure the change is visible.
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(file, later, later)
	if d.Has(project) {
		t.Fatal("removed breakpoint still detected")
	}
}
