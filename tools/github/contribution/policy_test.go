package contribution

import "testing"

func TestOtherAssignee(t *testing.T) {
	issue := &Issue{Assignees: []User{{Login: "contributor"}, {Login: "maintainer"}}}
	if !OtherAssignee(issue, "contributor") {
		t.Fatal("OtherAssignee() = false, want true")
	}
	if OtherAssignee(issue, "maintainer") == false {
		t.Fatal("OtherAssignee() = false, want true")
	}
	if OtherAssignee(&Issue{Assignees: []User{{Login: "contributor"}}}, "contributor") {
		t.Fatal("OtherAssignee() = true for the only assignee")
	}
	if OtherAssignee(nil, "contributor") {
		t.Fatal("OtherAssignee() = true for nil issue")
	}
}
