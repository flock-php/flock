// Package envutil merges environment variable lists.
package envutil

import (
	"runtime"
	"strings"
)

// Merge returns base with overrides applied: a key in overrides replaces every occurrence in base.
// Appending is not enough — with duplicate keys, getenv() returns the first one, so an inherited
// PHP_INI_SCAN_DIR (e.g. from another PHP install) would silently win.
func Merge(base, overrides []string) []string {
	keys := map[string]bool{}
	for _, kv := range overrides {
		keys[key(kv)] = true
	}

	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		if !keys[key(kv)] {
			out = append(out, kv)
		}
	}
	return append(out, overrides...)
}

func key(kv string) string {
	k, _, _ := strings.Cut(kv, "=")
	if runtime.GOOS == "windows" {
		return strings.ToUpper(k) // Windows env keys are case-insensitive
	}
	return k
}
