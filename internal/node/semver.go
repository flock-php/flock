package node

import (
	"regexp"
	"strings"
)

// operatorSpace joins an operator to its version: ">= 18" means ">=18".
var operatorSpace = regexp.MustCompile(`(>=|<=|>|<|=|\^|~)\s+`)

// Satisfies reports whether version ("22.11.0") is in an npm semver range such as
// "^20.17.0 || >=22.9.0", as packages state the Node versions they support (engines.node).
// It covers what engines ranges use: ||, space-separated comparators (>=, >, <=, <, =), ^, ~,
// x-ranges ("20.x", "*") and hyphen ranges ("18 - 20"). Pre-release versions are not handled.
func Satisfies(version, rng string) bool {
	v, ok := parse(version)
	if !ok || len(v) != 3 {
		return false
	}
	for _, alt := range strings.Split(rng, "||") {
		if satisfiesAll(v, strings.TrimSpace(alt)) {
			return true
		}
	}
	return false
}

func satisfiesAll(v []int, set string) bool {
	if set == "" || set == "*" || set == "x" {
		return true
	}
	if lo, hi, ok := strings.Cut(set, " - "); ok {
		return compare(v, lower(strings.TrimSpace(lo))) >= 0 && below(v, strings.TrimSpace(hi), true)
	}
	for _, c := range strings.Fields(operatorSpace.ReplaceAllString(set, "$1")) {
		if !satisfiesOne(v, c) {
			return false
		}
	}
	return true
}

func satisfiesOne(v []int, c string) bool {
	for _, op := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if rest, ok := strings.CutPrefix(c, op); ok {
			target := strings.TrimPrefix(strings.TrimSpace(rest), "v")
			switch op {
			case ">=":
				return compare(v, lower(target)) >= 0
			case ">":
				return !below(v, target, true) // above everything target covers
			case "<=":
				return below(v, target, true)
			case "<":
				return compare(v, lower(target)) < 0
			case "^":
				return compare(v, lower(target)) >= 0 && compare(v, caretLimit(target)) < 0
			case "~":
				return compare(v, lower(target)) >= 0 && compare(v, tildeLimit(target)) < 0
			default:
				return matchesPartial(v, target)
			}
		}
	}
	return matchesPartial(v, strings.TrimPrefix(c, "v"))
}

// parts reads "22", "22.9" or "22.9.0" (x/* for missing parts) and how many parts were given.
func parts(s string) ([3]int, int) {
	var out [3]int
	n := 0
	for i, p := range strings.SplitN(s, ".", 3) {
		if p == "x" || p == "X" || p == "*" || p == "" {
			break
		}
		num := 0
		for _, r := range p {
			if r < '0' || r > '9' {
				break
			}
			num = num*10 + int(r-'0')
		}
		out[i] = num
		n = i + 1
	}
	return out, n
}

func lower(s string) []int {
	p, _ := parts(s)
	return p[:]
}

// below reports whether v is below (or, with inclusive, within) everything a partial version covers:
// "<=20" allows all of 20.x.
func below(v []int, s string, inclusive bool) bool {
	p, n := parts(s)
	limit := p
	if n < 3 && n > 0 {
		limit[n-1]++
		for i := n; i < 3; i++ {
			limit[i] = 0
		}
		return compare(v, limit[:]) < 0
	}
	if inclusive {
		return compare(v, p[:]) <= 0
	}
	return compare(v, p[:]) < 0
}

func matchesPartial(v []int, s string) bool {
	p, n := parts(s)
	for i := range n {
		if v[i] != p[i] {
			return false
		}
	}
	return true
}

// caretLimit: ^1.2.3 → <2.0.0, ^0.2.3 → <0.3.0, ^0.0.3 → <0.0.4.
func caretLimit(s string) []int {
	p, n := parts(s)
	switch {
	case p[0] > 0 || n == 1:
		return []int{p[0] + 1, 0, 0}
	case p[1] > 0 || n == 2:
		return []int{0, p[1] + 1, 0}
	default:
		return []int{0, 0, p[2] + 1}
	}
}

// tildeLimit: ~1.2.3 → <1.3.0, ~1 → <2.0.0.
func tildeLimit(s string) []int {
	p, n := parts(s)
	if n <= 1 {
		return []int{p[0] + 1, 0, 0}
	}
	return []int{p[0], p[1] + 1, 0}
}

func compare(a, b []int) int {
	for i := range 3 {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}
