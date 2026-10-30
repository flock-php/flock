package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flock/internal/binaries"
	"flock/internal/config"
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

func TestPackageSuite(t *testing.T) {
	for version, want := range map[string]string{"1.0.0": "stable", "1.2.3": "stable", "1.0.0-beta.14": "beta", "1.1.0-rc.1": "beta", "dev": "beta"} {
		if got := packageSuite(version); got != want {
			t.Errorf("packageSuite(%s) = %s, want %s", version, got, want)
		}
	}
}

// A beta's package install puts the repository on beta, and the default channel follows it there.
func TestAdoptRepoChannel(t *testing.T) {
	dir := t.TempDir()
	oldApt, oldDnf, oldUnits, oldAur := aptSources, dnfRepo, packagedUnits, aurPackage
	aptSources, dnfRepo, packagedUnits = filepath.Join(dir, "flockphp.sources"), filepath.Join(dir, "flockphp.repo"), dir
	aurPackage = func() string { return "" }
	t.Cleanup(func() { aptSources, dnfRepo, packagedUnits, aurPackage = oldApt, oldDnf, oldUnits, oldAur })
	if err := os.WriteFile(filepath.Join(dir, "flock@.service"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	if err := os.WriteFile(aptSources, []byte(aptSourcesContent("stable")), 0o644); err != nil {
		t.Fatal(err)
	}
	if adoptRepoChannel(cfg); cfg.Channel != "" {
		t.Fatalf("stable suite: channel %q", cfg.Channel)
	}
	if err := os.WriteFile(dnfRepo, []byte(dnfRepoContent("beta")), 0o644); err != nil {
		t.Fatal(err)
	}
	if adoptRepoChannel(cfg); cfg.Channel != "beta" {
		t.Fatalf("beta suite: channel %q", cfg.Channel)
	}

	// A channel the user chose stays.
	cfg.Channel = "experimental"
	if adoptRepoChannel(cfg); cfg.Channel != "experimental" {
		t.Fatalf("experimental: channel %q", cfg.Channel)
	}
}

// On Arch, a channel names its AUR package, unless that one is installed already.
func TestAurChannelHint(t *testing.T) {
	if hint := aurChannelHint(aurStable, binaries.Beta); !strings.Contains(hint, "`yay -S flockphp-beta-bin`") || !strings.Contains(hint, "replaces flockphp-bin") {
		t.Fatalf("stable to beta: %q", hint)
	}
	if hint := aurChannelHint(aurBeta, binaries.Stable); !strings.Contains(hint, "`yay -S flockphp-bin`") || !strings.Contains(hint, "replaces flockphp-beta-bin") {
		t.Fatalf("beta to stable: %q", hint)
	}
	if hint := aurChannelHint(aurBeta, binaries.Experimental); hint != "" {
		t.Fatalf("experimental on the beta package: %q", hint)
	}
	if hint := aurChannelHint(aurStable, binaries.Stable); hint != "" {
		t.Fatalf("stable on the stable package: %q", hint)
	}
}
