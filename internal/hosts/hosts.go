// Package hosts resolves site domains on Windows, which has no equivalent of macOS'
// /etc/resolver: a domain can only be sent to Flock's DNS server through the hosts file.
//
// The daemon runs as the user and cannot edit the hosts file, so it writes the domains it
// serves to <root>/hosts.txt. A small Windows service (the "hosts helper", installed once by
// `flock install` with administrator rights) copies them into a marked block of the hosts file.
// The helper runs as LocalSystem and reads a file the user can write, so it only accepts
// strictly validated names under the TLD fixed at install time, always mapped to 127.0.0.1:
// the worst a rogue process could do is point other *.test names at this machine.
package hosts

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ServiceName is the Windows service of the hosts helper.
const ServiceName = "FlockHosts"

// MaxDomains caps how many entries the helper writes.
const MaxDomains = 2000

const (
	beginMarker = "# BEGIN Flock (managed by the Flock hosts helper, do not edit)"
	endMarker   = "# END Flock"
)

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// SourceFile is where the daemon lists the domains it serves.
func SourceFile(root string) string { return filepath.Join(root, "hosts.txt") }

// ValidDomain reports whether name is a hostname under tld that the helper may write.
func ValidDomain(name, tld string) bool {
	if len(name) > 253 || !label.MatchString(tld) {
		return false
	}
	rest, ok := strings.CutSuffix(name, "."+tld)
	if !ok || rest == "" {
		return false
	}
	for _, l := range strings.Split(rest, ".") {
		if !label.MatchString(l) {
			return false
		}
	}
	return true
}

// WriteSource stores the domains for the helper, leaving the file alone when nothing changed.
func WriteSource(root string, domains []string) error {
	sorted := slices.Clone(domains)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	data := []byte(strings.Join(sorted, "\n") + "\n")
	path := SourceFile(root)
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ParseSource returns the valid domains of a source file, in order, without duplicates.
func ParseSource(data []byte, tld string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		name := strings.ToLower(strings.TrimSpace(line))
		if seen[name] || !ValidDomain(name, tld) {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) == MaxDomains {
			break
		}
	}
	return out
}

// Render returns the hosts file with Flock's block replaced by entries for domains
// (or removed, when domains is empty). Everything outside the block is kept as is.
func Render(current []byte, domains []string) []byte {
	text := strings.ReplaceAll(string(current), "\r\n", "\n")

	if start := strings.Index(text, beginMarker); start >= 0 {
		end := strings.Index(text[start:], endMarker)
		if end < 0 {
			text = text[:start] // a truncated block: drop it to the end
		} else {
			end += start + len(endMarker)
			text = text[:start] + strings.TrimPrefix(text[end:], "\n")
		}
	}
	text = strings.TrimRight(text, "\n")

	if len(domains) > 0 {
		var b strings.Builder
		b.WriteString(beginMarker + "\n")
		for _, d := range domains {
			b.WriteString("127.0.0.1 " + d + "\n")
		}
		b.WriteString(endMarker)
		if text != "" {
			text += "\n\n"
		}
		text += b.String()
	}

	return []byte(strings.ReplaceAll(text, "\n", "\r\n") + "\r\n")
}
