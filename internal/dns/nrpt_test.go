package dns

import (
	"slices"
	"strings"
	"testing"
)

// The NRPT command layer is pure strings: the PowerShell itself is never run in tests (it needs
// Windows and administrator rights), only the script it composes.
func TestNRPTSetScriptOneRulePerSuffix(t *testing.T) {
	script, err := nrptSetScript([]string{"test", "loc"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Add-DnsClientNrptRule -Namespace '.test' -NameServers '127.0.0.1' -Comment 'Flock'; `,
		`Add-DnsClientNrptRule -Namespace '.loc' -NameServers '127.0.0.1' -Comment 'Flock'; `,
		`Clear-DnsClientCache`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("the script lacks %q:\n%s", want, script)
		}
	}
	// Every Flock rule goes first (one sweep), then the per-suffix adds.
	if n := strings.Count(script, removeRulesScript); n != 1 {
		t.Fatalf("the removal runs %d times:\n%s", n, script)
	}
	if strings.Index(script, removeRulesScript) > strings.Index(script, "Add-DnsClientNrptRule") {
		t.Fatalf("the adds come before the removal:\n%s", script)
	}

	for _, tlds := range [][]string{{}, {"test", "not valid"}, {"test", "a.b"}, {"test", "-x"}, {"test", "x-"}} {
		if _, err := nrptSetScript(tlds); err == nil {
			t.Errorf("%q: accepted", tlds)
		}
	}
}

// The check reads the rules back: a namespace counts only when Flock's rule sends it to 127.0.0.1.
func TestNRPTRuleNamespaces(t *testing.T) {
	out := ".test|127.0.0.1\n.a,.b|127.0.0.1\n.loc|10.0.0.2\n\n"
	set := nrptRuleNamespaces(out)
	for _, ns := range []string{".test", ".a", ".b"} {
		if !set[ns] {
			t.Errorf("%s: not seen as Flock's rule: %v", ns, set)
		}
	}
	if set[".loc"] {
		t.Errorf(".loc points at 10.0.0.2, not Flock: %v", set)
	}
}

// StaleNRPTRuleSuffixes reads Flock's rule dump and lists single-label namespaces outside the
// active set; multi-label namespaces and rules pointing elsewhere (not 127.0.0.1) are ignored.
func TestStaleNRPTRuleSuffixes(t *testing.T) {
	out := ".loc|127.0.0.1\n.old|127.0.0.1\n.a.b|127.0.0.1\n.x|8.8.8.8\n\n"
	if got := StaleNRPTRuleSuffixes(out, []string{"loc"}); !slices.Equal(got, []string{"old"}) {
		t.Fatalf("got %v, want [old]", got)
	}
	if got := StaleNRPTRuleSuffixes(out, nil); !slices.Equal(got, []string{"loc", "old"}) {
		t.Fatalf("an empty active set: %v", got)
	}
	if got := StaleNRPTRuleSuffixes("", []string{"loc"}); got != nil {
		t.Fatalf("no rules: %v", got)
	}
}
