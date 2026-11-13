package issue_reference

import "testing"

func TestParsePreservesBodyOrder(t *testing.T) {
	got := Parse("Fixes flock-php/binaries#72 and #73", "flock")
	want := []Reference{{Repository: "binaries", Number: 72}, {Repository: "flock", Number: 73}}
	if len(got) != len(want) {
		t.Fatalf("Parse() = %v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Parse() = %v, want %v", got, want)
		}
	}
}
