package hosts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestValidDomainOnlyAcceptsNamesUnderTheTLD(t *testing.T) {
	for name, want := range map[string]bool{
		"app.test":           true,
		"api.my-app.test":    true,
		"test":               false,
		".test":              false,
		"app.test.evil.com":  false,
		"bank.com":           false,
		"app..test":          false,
		"-app.test":          false,
		"app-.test":          false,
		"APP.test":           false, // ParseSource lowercases first
		"app.test\n1.2.3.4":  false,
		"app test.test":      false,
		"xn--bcher-kva.test": true,
	} {
		if got := ValidDomain(name, "test"); got != want {
			t.Errorf("%q: got %v, want %v", name, got, want)
		}
	}
	if ValidDomain("app.com", "c.om") {
		t.Error("an invalid TLD must reject everything")
	}
}

func TestParseSourceDropsInvalidAndDuplicateNames(t *testing.T) {
	got := ParseSource([]byte("app.test\r\nApp.Test\n  shop.test \nbank.com\n0.0.0.0 evil.test\n\n"), "test")
	if want := []string{"app.test", "shop.test"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRenderReplacesOnlyFlocksBlock(t *testing.T) {
	original := "# Copyright (c) Microsoft\r\n127.0.0.1 localhost\r\n"

	once := Render([]byte(original), []string{"a.test", "b.test"})
	want := "# Copyright (c) Microsoft\r\n127.0.0.1 localhost\r\n\r\n" + beginMarker + "\r\n127.0.0.1 a.test\r\n127.0.0.1 b.test\r\n" + endMarker + "\r\n"
	if string(once) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", once, want)
	}

	// Lines the user added after the block survive an update.
	edited := string(once) + "10.0.0.5 nas\r\n"
	twice := string(Render([]byte(edited), []string{"c.test"}))
	if strings.Contains(twice, "a.test") || !strings.Contains(twice, "127.0.0.1 c.test") || !strings.Contains(twice, "10.0.0.5 nas") {
		t.Fatalf("update lost or kept the wrong lines:\n%s", twice)
	}

	if removed := string(Render([]byte(twice), nil)); strings.Contains(removed, "Flock") || !strings.Contains(removed, "10.0.0.5 nas") || !strings.HasPrefix(removed, original) {
		t.Fatalf("removing the block changed other lines:\n%q", removed)
	}
}

func TestWriteSourceSortsAndSkipsUnchangedWrites(t *testing.T) {
	root := t.TempDir()
	if err := WriteSource(root, []string{"b.test", "a.test", "b.test"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "hosts.txt"))
	if string(data) != "a.test\nb.test\n" {
		t.Fatalf("got %q", data)
	}
}

func TestBlockedRemembersTheRefusedExecutableOnly(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, HelperName)
	if err := os.WriteFile(exe, []byte("build 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Blocked(root) || BlockedExe(root, exe) {
		t.Fatal("nothing was blocked yet")
	}

	if err := MarkBlocked(root, exe); err != nil {
		t.Fatal(err)
	}
	if !Blocked(root) || !BlockedExe(root, exe) {
		t.Fatal("want the refused helper remembered")
	}

	// Smart App Control judges each file on its own: another build gets another try.
	if err := os.WriteFile(exe, []byte("build 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if BlockedExe(root, exe) {
		t.Error("a different build counts as blocked")
	}
	if !Blocked(root) {
		t.Error("the record went away before the new build was tried")
	}

	ClearBlocked(root)
	if Blocked(root) {
		t.Error("ClearBlocked kept the record")
	}
}
