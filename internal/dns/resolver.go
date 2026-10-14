package dns

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResolverDir is where macOS reads per-domain resolver files (resolver(5)). Tests move it.
var ResolverDir = "/etc/resolver"

// ResolverFile is the resolver file `flock install` writes on macOS to send tld to Flock.
func ResolverFile(tld string) string { return filepath.Join(ResolverDir, tld) }

// ResolverContent is what ResolverFile holds when it sends lookups to Flock's DNS server on port.
func ResolverContent(port int) string {
	return fmt.Sprintf("# Managed by Flock\nnameserver 127.0.0.1\nport %d\n", port)
}

// ResolverProblem explains why the resolver file for tld does not send lookups to Flock's DNS
// server on port, or returns "" when it does.
func ResolverProblem(tld string, port int) string {
	path := ResolverFile(tld)
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("%s is missing, so macOS doesn't send .%s lookups to Flock — run `flock install`", path, tld)
	case err != nil:
		return fmt.Sprintf("can't read %s (%v) — run `flock install`", path, err)
	case string(data) != ResolverContent(port):
		return fmt.Sprintf("%s doesn't point at Flock's DNS server on port %d — run `flock install`", path, port)
	}
	return ""
}
