package cli

import (
	"bytes"
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGUISubsystemPatchesAConsoleExecutable(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles a Windows binary")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "hello.exe")
	build := exec.Command("go", "build", "-o", exe, "main.go")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "GO111MODULE=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cannot cross-compile: %v\n%s", err, out)
	}

	data, _ := os.ReadFile(exe)
	patched, err := guiSubsystem(data)
	if err != nil {
		t.Fatal(err)
	}
	f, err := pe.NewFile(bytes.NewReader(patched))
	if err != nil {
		t.Fatal(err)
	}
	if sub := f.OptionalHeader.(*pe.OptionalHeader64).Subsystem; sub != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		t.Fatalf("subsystem %d", sub)
	}
	if diff := countDiff(data, patched); diff != 1 {
		t.Fatalf("expected exactly one changed byte, got %d", diff)
	}
	if _, err := guiSubsystem([]byte("#!/bin/sh\n")); err == nil {
		t.Fatal("a non-PE file must be rejected")
	}
}

func countDiff(a, b []byte) int {
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}
