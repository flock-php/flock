package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"flock/internal/binaries"
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
func writeRepo(suite string, ifMissing bool) error {
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

// followChannel switches the repository's suite with the channel, so Flock itself updates from it
// as on macOS and Windows: the file belongs to the system, so it asks for a password. Only the
// package's Flock has the repository.
func followChannel(cmd *cobra.Command, ch binaries.Channel) error {
	if !packaged() {
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
