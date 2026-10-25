package dns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResolvedDir is where systemd-resolved reads drop-in configuration (resolved.conf(5)). Tests move it.
var ResolvedDir = "/etc/systemd/resolved.conf.d"

// ResolvedFile is the drop-in `flock install` writes on Linux to send the TLD to Flock.
func ResolvedFile() string { return filepath.Join(ResolvedDir, "flock.conf") }

// ResolvedContent is what ResolvedFile holds when it sends lookups under tld to Flock's DNS server
// on port. "~tld" makes it a routing-only domain: resolved asks Flock for those names only and
// doesn't append the TLD to single-label lookups. Under SELinux it also says that the port is
// labelled for DNS, which only root can look up: a drop-in without that line sets it up again.
func ResolvedContent(tld string, port int) string {
	var selinux string
	if SELinux() {
		selinux = fmt.Sprintf("# SELinux: port %d is labelled dns_port_t, so systemd-resolved may query it\n", port)
	}
	return fmt.Sprintf("# Managed by Flock\n%s[Resolve]\nDNS=127.0.0.1:%d\nDomains=~%s\n", selinux, port, tld)
}

// SELinuxDir is where the kernel mounts selinuxfs when SELinux is on. Tests move it.
var SELinuxDir = "/sys/fs/selinux"

// SELinux reports whether SELinux is on (enforcing or permissive), as on Fedora. Its policy only
// lets systemd-resolved query DNS ports, and Flock's isn't one until it is labelled.
func SELinux() bool {
	_, err := os.Stat(filepath.Join(SELinuxDir, "enforce"))
	return err == nil
}

// ResolvedProblem explains why the drop-in doesn't send lookups under tld to Flock's DNS server on
// port, or returns "" when it does.
func ResolvedProblem(tld string, port int) string {
	path := ResolvedFile()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("%s is missing, so systemd-resolved doesn't send .%s lookups to Flock — run `flock install`", path, tld)
	case err != nil:
		return fmt.Sprintf("can't read %s (%v) — run `flock install`", path, err)
	case string(data) != ResolvedContent(tld, port):
		return fmt.Sprintf("%s doesn't point at Flock's DNS server on port %d — run `flock install`", path, port)
	}
	return ""
}

// ResolvedRunning reports whether systemd-resolved answers the system's lookups: its stub
// resolver's directory exists while it runs.
func ResolvedRunning() bool {
	_, err := os.Stat("/run/systemd/resolve")
	return err == nil
}
