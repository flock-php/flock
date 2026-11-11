package issue_reference

import "testing"

func TestReferenceString(t *testing.T) {
	if got := (Reference{Repository: "flock", Number: 73}).String(); got != "flock#73" {
		t.Fatalf("String() = %q", got)
	}
}
