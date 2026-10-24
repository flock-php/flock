package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/binaries"
)

// The repository's file goes where the package manager is, and --if-missing keeps a chosen suite.
func TestWriteRepo(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	oldApt, oldDnf := aptSources, dnfRepo
	aptSources, dnfRepo = filepath.Join(dir, "apt", "flockphp.sources"), filepath.Join(dir, "yum", "flockphp.repo")
	t.Cleanup(func() { aptSources, dnfRepo = oldApt, oldDnf })

	// Only apt here: no dnf file.
	if err := os.WriteFile(filepath.Join(bin, "apt-get"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRepo("beta", false); err != nil {
		t.Fatal(err)
	}
	if !fileHas(aptSources, aptSourcesContent("beta")) || fileExists(dnfRepo) {
		t.Fatal("expected only apt's file, for beta")
	}
	if !strings.Contains(aptSourcesContent("beta"), "URIs: https://packages.flockphp.com/apt/beta/\n") {
		t.Fatalf("apt sources: %s", aptSourcesContent("beta"))
	}

	// The package's install keeps the channel a user chose.
	if err := writeRepo("stable", true); err != nil {
		t.Fatal(err)
	}
	if !fileHas(aptSources, aptSourcesContent("beta")) {
		t.Fatal("--if-missing replaced the suite")
	}

	if err := os.WriteFile(filepath.Join(bin, "dnf"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRepo("stable", false); err != nil {
		t.Fatal(err)
	}
	if !fileHas(aptSources, aptSourcesContent("stable")) || !fileHas(dnfRepo, dnfRepoContent("stable")) {
		t.Fatal("expected both files, for stable")
	}
	if err := writeRepo("nightly", false); err == nil {
		t.Fatal("accepted an unknown suite")
	}
}

func TestRepoSuite(t *testing.T) {
	for ch, want := range map[binaries.Channel]string{binaries.Stable: "stable", binaries.Beta: "beta", binaries.Experimental: "beta"} {
		if got := repoSuite(ch); got != want {
			t.Errorf("repoSuite(%s) = %s, want %s", ch, got, want)
		}
	}
}
