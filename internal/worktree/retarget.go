package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"flock/internal/config"
)

// retargetKeys are the .env keys whose values name a site's host, the ones a TLD change moves:
// the app URL, the session cookie's domain, Sanctum's stateful-domain list and the mail sender.
var retargetKeys = []string{"APP_URL", "SESSION_DOMAIN", "SANCTUM_STATEFUL_DOMAINS", "MAIL_FROM"}

// RetargetDotenv rewrites the site host in one .env: in the keys of retargetKeys, oldHost
// becomes newHost. The match is by host, not by substring: the value is the host itself (with
// the leading dot SESSION_DOMAIN uses for subdomain cookies), a member of a comma list (its
// host part, :port kept), the domain of a user@host address, or the host of a URL, in which a
// deeper host under oldHost (x.acme.test in https://x.acme.test/y) swaps too because Flock
// serves a site's subdomains under it as well — the same suffix-aware host matching the
// worktree setup of writeEnv performs, generalized to old→new. Matching only at host boundaries
// keeps acme.test.evil.com from being rewritten just because it contains the old host. Every
// other key, comment and line stays exactly as written — comments are prose, and the other keys
// are not Flock's domains — and the file is written only when something changed. A missing
// file is a no-op, not an error: a site folder without a .env has nothing to retarget.
func RetargetDotenv(path, oldHost, newHost string) (changed []string, err error) {
	env, err := ReadDotenv(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for i, line := range env.lines {
		key, raw, prefix, ok := assignment(line)
		if !ok || !slices.Contains(retargetKeys, key) {
			continue
		}
		newRaw, swapped := swapRawValue(key, raw, oldHost, newHost)
		if !swapped {
			continue
		}
		// Every assignment of the key is rewritten, not just the last one dotenv parsers read:
		// a shadowed line left on the old host would resurface the moment the key moves back.
		env.lines[i] = prefix + key + "=" + newRaw
		if !slices.Contains(changed, key) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		return nil, nil
	}
	return changed, env.Write(path)
}

// assignment splits an assignment line into its key, its raw value as written (quotes and
// trailing comment included, trimmed) and its "export " prefix, like Dotenv.line does; comment
// lines, blanks and non-assignments give ok = false.
func assignment(line string) (key, raw, prefix string, ok bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", "", "", false
	}
	hasExport := false
	t, hasExport = strings.CutPrefix(t, "export ")
	name, raw, ok := strings.Cut(t, "=")
	if !ok {
		return "", "", "", false
	}
	prefix = ""
	if hasExport {
		prefix = "export "
	}
	return strings.TrimSpace(name), strings.TrimSpace(raw), prefix, true
}

// swapRawValue swaps the hosts in a line's raw value, keeping its quoting (a host never
// contains a quote character, so the value as written can be rewritten in place) and the
// trailing comment of an unquoted value (the " #", as Get splits it).
func swapRawValue(key, raw, oldHost, newHost string) (string, bool) {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') {
		if end := strings.IndexByte(raw[1:], raw[0]); end >= 0 {
			interior, rest := raw[1:1+end], raw[1+end:]
			if swapped, ok := swapValue(interior, key, oldHost, newHost); ok {
				return raw[:1] + swapped + rest, true
			}
			return raw, false
		}
	}
	value, comment := raw, ""
	if i := strings.Index(raw, " #"); i >= 0 {
		value, comment = raw[:i], raw[i:]
	}
	if swapped, ok := swapValue(value, key, oldHost, newHost); ok {
		return swapped + comment, true
	}
	return raw, false
}

// swapValue applies the host swap to one key's value: a bare host, a comma list of hosts, URLs
// anywhere in it, and the address in MAIL_FROM.
func swapValue(value, key, oldHost, newHost string) (string, bool) {
	switch value {
	case oldHost:
		return newHost, true
	case "." + oldHost:
		return "." + newHost, true
	}
	out := swapURLs(value, oldHost, newHost)
	if strings.Contains(out, ",") {
		parts := strings.Split(out, ",")
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" || strings.Contains(p, "://") {
				continue // URLs in a list are already swapped
			}
			leading, host := "", p
			if rest, isDot := strings.CutPrefix(p, "."); isDot {
				leading, host = ".", rest
			}
			h, port, withPort := strings.Cut(host, ":")
			if h != oldHost {
				continue
			}
			parts[i] = leading + newHost
			if withPort {
				parts[i] += ":" + port
			}
		}
		out = strings.Join(parts, ",")
	}
	if key == "MAIL_FROM" {
		if i := strings.LastIndex(out, "@"); i >= 0 {
			switch domain := out[i+1:]; domain {
			case oldHost:
				out = out[:i+1] + newHost
			case "." + oldHost:
				out = out[:i+1] + "." + newHost
			}
		}
	}
	return out, out != value
}

// urlToken is a URL inside a value: a scheme, "://" and the run of characters that end it
// (whitespace, a comma, a quote or a semicolon).
var urlToken = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://[^\s"',;]+`)

// swapURLs swaps the host of every URL in a value, userinfo, :port, path and query kept.
func swapURLs(value, oldHost, newHost string) string {
	return urlToken.ReplaceAllStringFunc(value, func(u string) string {
		i := strings.Index(u, "://") + len("://")
		tail := u[i:]
		auth, rest := tail, ""
		if j := strings.IndexAny(tail, "/?#"); j >= 0 {
			auth, rest = tail[:j], tail[j:]
		}
		userinfo := ""
		if at := strings.LastIndex(auth, "@"); at >= 0 {
			userinfo, auth = auth[:at+1], auth[at+1:]
		}
		host, port := auth, ""
		if c := strings.Index(auth, ":"); c >= 0 {
			host, port = auth[:c], auth[c:]
		}
		swapped, ok := swapHostSuffix(host, oldHost, newHost)
		if !ok {
			return u
		}
		return u[:i] + userinfo + swapped + port + rest
	})
}

// swapHostSuffix swaps a URL's host when it equals oldHost, and when it sits deeper under it
// (x.acme.test → x.acme.loc): Flock serves subdomains of a site, so an embedded URL under the
// old host belongs to the site too; a host that only ends in the old host's letters
// (evil-acme.test) does not.
func swapHostSuffix(host, oldHost, newHost string) (string, bool) {
	switch {
	case host == oldHost:
		return newHost, true
	case strings.HasSuffix(host, "."+oldHost):
		return strings.TrimSuffix(host, oldHost) + newHost, true
	}
	return host, false
}

// HostSwap is one .env file to retarget when the local TLD changes: the file's path and the
// hosts to swap in it.
type HostSwap struct {
	Path string // the .env file, under the site's or worktree's folder
	Old  string // the host as the TLD change leaves it: <name>.<oldTLD>, <slug>.<parent>.<oldTLD>
	New  string // the host to write in its place
}

// TLDHostSwaps lists the .env files a TLD change from oldTLD to newTLD retargets: every linked
// site (the .env under its Path, served at <name>.<TLD>) and every stored worktree entry (its
// .env, served at <slug>.<parent>.<TLD>). Every host is derived from the arguments, never from
// c.TLD: callers may compute the list after Config.SetTLD has already stored the new TLD, and
// reading c.TLD for the old side would then give both sides the new suffix. Sites without a
// .env are listed as well: RetargetDotenv treats a missing file as a no-op. (A free function,
// not a method: Go forbids defining methods on a type from another package, and the worktree
// package is where the .env rewriting lives.)
func TLDHostSwaps(c *config.Config, oldTLD, newTLD string) []HostSwap {
	var swaps []HostSwap
	for _, s := range c.Sites {
		swaps = append(swaps, HostSwap{
			Path: filepath.Join(s.Path, ".env"),
			Old:  s.Name + "." + oldTLD,
			New:  s.Name + "." + newTLD,
		})
	}
	for _, e := range c.Worktrees {
		if e.Slug == "" || e.Site == "" {
			continue // no stored slug and parent: no host named it
		}
		swaps = append(swaps, HostSwap{
			Path: filepath.Join(e.Path, ".env"),
			Old:  e.Slug + "." + e.Site + "." + oldTLD,
			New:  e.Slug + "." + e.Site + "." + newTLD,
		})
	}
	return swaps
}
