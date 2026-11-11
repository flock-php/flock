package dns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ResolvedDir is where systemd-resolved reads drop-in configuration (resolved.conf(5)). Tests move it.
var ResolvedDir = "/etc/systemd/resolved.conf.d"

// ResolvedFile is the drop-in `flock install` writes on Linux to send the active suffixes to Flock.
func ResolvedFile() string { return filepath.Join(ResolvedDir, "flock.conf") }

// ResolvedContent is what ResolvedFile holds when it sends lookups under every suffix in tlds to
// Flock's DNS server on port. "~suffix" makes it a routing-only domain: resolved asks Flock for
// those names only and doesn't append the suffix to single-label lookups. One drop-in carries the
// whole set. Under SELinux it also says that the port is labelled for DNS, which only root can
// look up: a drop-in without that line sets it up again.
func ResolvedContent(tlds []string, port int) string {
	var selinux string
	if SELinux() {
		selinux = fmt.Sprintf("# SELinux: port %d is labelled dns_port_t, so systemd-resolved may query it\n", port)
	}
	domains := make([]string, 0, len(tlds))
	for _, tld := range normalizeSuffixes(tlds) {
		domains = append(domains, "~"+tld)
	}
	return fmt.Sprintf("%s\n%s[Resolve]\nDNS=127.0.0.1:%d\nDomains=%s\n", ResolverMarker, selinux, port, strings.Join(domains, " "))
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
// port, or returns "" when it does. The drop-in routes the whole active-suffix set; this checks
// one suffix's routing in it, so one problem names the missing or foreign config.
func ResolvedProblem(tld string, port int) string {
	path := ResolvedFile()
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("%s is missing, so systemd-resolved doesn't send .%s lookups to Flock — run `flock install`", path, tld)
	case err != nil:
		return fmt.Sprintf("can't read %s (%v) — run `flock install`", path, err)
	case !resolvedSends(string(data), tld, port):
		return fmt.Sprintf("%s doesn't point at Flock's DNS server on port %d — run `flock install`", path, port)
	}
	return ""
}

// resolvedSends reports whether a drop-in's [Resolve] routes .tld (routing-only, "~tld") to
// Flock's server at 127.0.0.1:port.
func resolvedSends(content, tld string, port int) bool {
	dns, routed := false, false
	for line := range strings.Lines(content) {
		if rest, ok := strings.CutPrefix(line, "DNS=127.0.0.1:"); ok && strings.TrimSpace(rest) == strconv.Itoa(port) {
			dns = true
		}
		if rest, ok := strings.CutPrefix(line, "Domains="); ok {
			for _, d := range strings.Fields(rest) {
				if d == "~"+tld {
					routed = true
				}
			}
		}
	}
	return dns && routed
}

// ResolvedRunning reports whether systemd-resolved answers the system's lookups: its stub
// resolver's directory exists while it runs.
func ResolvedRunning() bool {
	_, err := os.Stat("/run/systemd/resolve")
	return err == nil
}
