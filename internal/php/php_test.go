package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetXdebugWritesAndRemovesTheIni(t *testing.T) {
	root := t.TempDir()

	if err := SetXdebug(root, "8.5", true); err == nil || !strings.Contains(err.Error(), "without xdebug") {
		t.Fatalf("missing xdebug.so must be explained, got %v", err)
	}

	so := ExtensionPath(root, "8.5", "xdebug")
	if err := os.MkdirAll(filepath.Dir(so), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(so, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SetXdebug(root, "8.5", true); err != nil || !XdebugEnabled(root, "8.5") {
		t.Fatalf("enable: %v", err)
	}
	data, _ := os.ReadFile(xdebugIni(root, "8.5"))
	if !strings.Contains(string(data), `zend_extension = "`+so+`"`) || !strings.Contains(string(data), "start_with_request = trigger") {
		t.Fatalf("unexpected ini:\n%s", data)
	}

	if err := SetXdebug(root, "8.5", false); err != nil || XdebugEnabled(root, "8.5") {
		t.Fatalf("disable: %v", err)
	}
	if err := SetXdebug(root, "8.5", false); err != nil {
		t.Fatalf("disabling twice must be fine: %v", err)
	}
}

func TestFPMSpecChangesWhenXdebugIsToggled(t *testing.T) {
	root := t.TempDir()
	so := ExtensionPath(root, "8.5", "xdebug")
	_ = os.MkdirAll(filepath.Dir(so), 0o755)
	_ = os.WriteFile(so, nil, 0o755)

	off, err := FPMSpec(root, "8.5")
	if err != nil {
		t.Fatal(err)
	}
	_ = SetXdebug(root, "8.5", true)
	on, _ := FPMSpec(root, "8.5")

	if strings.Join(off.Env, " ") == strings.Join(on.Env, " ") {
		t.Fatal("spec must differ so the supervisor restarts FPM")
	}
}
