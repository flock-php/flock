package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"flock/internal/binaries"
	"flock/internal/config"
)

// Flock's apt and dnf repository (packages.flockphp.com, see linux/packaging/repository.md), which
// the package adds on install like Chrome's does, so Flock updates with the system's updates. It
// has a suite per channel: stable, and beta (betas, and stable releases once they are newer). The
// package removes the file when it is purged (postremove.sh).

const repoURL = "https://packages.flockphp.com"

// Where the package puts the repository's key, and where the repository's file goes. Tests move them.
var (
	aptSources = "/etc/apt/sources.list.d/flockphp.sources"
	dnfRepo    = "/etc/yum.repos.d/flockphp.repo"
)

const (
	aptKey = "/usr/share/keyrings/flockphp.asc"
	dnfKey = "/etc/pki/rpm-gpg/RPM-GPG-KEY-flockphp"
)

// repoSuite is the suite Flock's own updates come from on a channel: experimental takes betas too.
func repoSuite(ch binaries.Channel) string {
	if ch == binaries.Stable {
		return "stable"
	}
	return "beta"
}

// packageSuite is the suite the package's install starts on: a beta's (any prerelease version, and
// dev builds) is beta, so a beta installed before the first stable release finds a suite.
func packageSuite(version string) string {
	if strings.Contains(version, "-") || version == "dev" {
		return "beta"
	}
	return "stable"
}

// adoptRepoChannel moves a user still on the default channel to beta when the repository's file
// is on the beta suite (the install of a beta), so the app's Beta versions switch shows it.
func adoptRepoChannel(cfg *config.Config) {
	if cfg.Channel != "" || !packaged() || aurPackage() != "" {
		return
	}
	if fileHas(aptSources, aptSourcesContent("beta")) || fileHas(dnfRepo, dnfRepoContent("beta")) {
		cfg.Channel = string(binaries.Beta)
	}
}

func aptSourcesContent(suite string) string {
	return fmt.Sprintf(`# Managed by Flock: `+"`flock channel`"+` switches the suite (stable or beta).
Types: deb
URIs: %s/apt/%s/
Suites: ./
Signed-By: %s
`, repoURL, suite, aptKey)
}

func dnfRepoContent(suite string) string {
	return fmt.Sprintf(`# Managed by Flock: `+"`flock channel`"+` switches the suite (stable or beta).
[flockphp]
name=Flock
baseurl=%s/rpm/%s
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file://%s
`, repoURL, suite, dnfKey)
}

// writeRepo points the system's package manager at suite: apt's sources where apt is, dnf's repo
// where dnf is. ifMissing leaves an existing file alone (the package's install keeps the channel).
// "auto" is packageSuite of this flock's version.
func writeRepo(suite string, ifMissing bool) error {
	if suite == "auto" {
		suite = packageSuite(Version)
	}
	if suite != "stable" && suite != "beta" {
		return fmt.Errorf("invalid suite %q", suite)
	}
	var errs []error
	write := func(path, content string) {
		if ifMissing && fileExists(path) {
			return
		}
		errs = append(errs, writeOwnFile(path, content))
	}
	if hasCommand("apt-get") {
		write(aptSources, aptSourcesContent(suite))
	}
	if hasCommand("dnf") {
		write(dnfRepo, dnfRepoContent(suite))
	}
	return errors.Join(errs...)
}

// On Arch, Flock is an AUR package instead, one per channel: flockphp-bin and flockphp-beta-bin
// (linux/packaging/aur), which AUR helpers update.
const (
	aurStable = "flockphp-bin"
	aurBeta   = "flockphp-beta-bin"
)

// aurPackage is the AUR package Flock is installed from, or "" (not Arch, or not from the AUR). A
// variable: tests replace it.
var aurPackage = func() string {
	if !hasCommand("pacman") {
		return ""
	}
	out, err := exec.Command("pacman", "-Qqo", packagedBinary).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// aurChannelHint tells how Flock itself follows the channel on Arch, where switching means
// installing the channel's AUR package; "" when the one installed is it already.
func aurChannelHint(installed string, ch binaries.Channel) string {
	want, versions := aurStable, "stable versions"
	if ch != binaries.Stable {
		want, versions = aurBeta, "betas"
	}
	if installed == want {
		return ""
	}
	return fmt.Sprintf("Flock's own %s are the AUR package %s: install it with your AUR helper (e.g. `yay -S %s`), which replaces %s.", versions, want, want, installed)
}

// followChannel switches the repository's suite with the channel, so Flock itself updates from it
// as on macOS and Windows: the file belongs to the system, so it asks for a password. Only the
// package's Flock has the repository; on Arch, it tells which AUR package to install instead.
func followChannel(cmd *cobra.Command, ch binaries.Channel) error {
	if !packaged() {
		return nil
	}
	if pkg := aurPackage(); pkg != "" {
		if hint := aurChannelHint(pkg, ch); hint != "" {
			cmd.Println(hint)
		}
		return nil
	}
	suite := repoSuite(ch)
	if (!hasCommand("apt-get") || fileHas(aptSources, aptSourcesContent(suite))) &&
		(!hasCommand("dnf") || fileHas(dnfRepo, dnfRepoContent(suite))) {
		return nil
	}
	if err := runPrivileged(cmd, packagedBinary, "privileged-setup", "--repo", suite); err != nil {
		return fmt.Errorf("switch Flock's package repository to %s: %w", suite, err)
	}
	if ch != binaries.Stable {
		cmd.Println("Flock's own updates come from its beta suite: `flock update` installs the newest beta.")
	}
	return nil
}
