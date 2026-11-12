package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestIssueMachineLabelsPlatformsBeforeTriage(t *testing.T) {
	event := issueEvent{Action: "opened", Issue: issue{Number: 73, User: user{Login: "contributor", Type: "User"}, Body: "### Platform\n\nmacOS, Linux"}}
	machine := newIssueMachine("flock-php", "flock", event)
	steps := []struct {
		result commandResult
		kind   commandKind
		label  string
	}{
		{commandResult{}, commandAddIssueLabel, "os: mac"},
		{commandResult{}, commandAddIssueLabel, "os: linux"},
		{commandResult{}, commandReadRepositoryPermission, ""},
		{commandResult{permission: "read"}, commandAddIssueLabel, "needs triage"},
		{commandResult{}, commandDone, ""},
	}
	for index, expected := range steps {
		command, err := machine.step(expected.result)
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		if command.kind != expected.kind || command.label != expected.label {
			t.Fatalf("step %d: command = %+v, want kind=%d label=%q", index, command, expected.kind, expected.label)
		}
	}
}

func TestIssueMachineBypassesTriageForPlatformsAndPermissions(t *testing.T) {
	for _, issue := range []issue{
		{Number: 73, User: user{Type: "Bot"}},
		{Number: 73, AuthorAssociation: "MEMBER"},
	} {
		machine := newIssueMachine("flock-php", "flock", issueEvent{Action: "opened", Issue: issue})
		command, err := machine.step(commandResult{})
		if err != nil {
			t.Fatal(err)
		}
		if command.kind != commandReadRepositoryPermission {
			t.Fatalf("initial command = %d, want permission read", command.kind)
		}
		permission := "read"
		if issue.AuthorAssociation == "MEMBER" {
			permission = "admin"
		}
		command, err = machine.step(commandResult{permission: permission})
		if err != nil {
			t.Fatal(err)
		}
		if command.kind != commandDone {
			t.Fatalf("bypass command = %d, want done", command.kind)
		}
	}
}

func TestIssueMachineUsesCurrentAssignmentAndCleansSupersededClaim(t *testing.T) {
	event := issueEvent{Action: "assigned", Issue: issue{Number: 73, Assignees: []user{{Login: "contributor"}}}}
	machine := newIssueMachine("flock-php", "flock", event)
	commands := []commandKind{commandReadIssue, commandReadIssueClaim, commandRemoveIssueLabel, commandRemoveIssueLabel, commandDone}
	results := []commandResult{
		{},
		{issue: &issue{Number: 73, Assignees: []user{{Login: "contributor"}, {Login: "maintainer"}}, Labels: []label{{Name: helpWantedLabel}, {Name: claimedLabel}}}},
		{claim: &claim{PullRequestNumber: 87, Author: "contributor"}},
		{},
		{},
	}
	for index, want := range commands {
		command, err := machine.step(results[index])
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		if command.kind != want {
			t.Fatalf("step %d command = %d, want %d", index, command.kind, want)
		}
	}
}

func TestIssueMachineSkipsAssignmentCleanupWhenCurrentIssueIsUnassigned(t *testing.T) {
	machine := newIssueMachine("flock-php", "flock", issueEvent{Action: "assigned", Issue: issue{Number: 73, Assignees: []user{{Login: "contributor"}}}})
	if command, _ := machine.step(commandResult{}); command.kind != commandReadIssue {
		t.Fatalf("initial command = %d", command.kind)
	}
	if command, _ := machine.step(commandResult{issue: &issue{Number: 73}}); command.kind != commandDone {
		t.Fatalf("unassigned current issue command = %d", command.kind)
	}
}

func TestIssueMachinePropagatesCommandErrorsAndRejectsUnknownStates(t *testing.T) {
	machine := newIssueMachine("flock-php", "flock", issueEvent{Action: "opened", Issue: issue{Number: 73}})
	want := errors.New("permission")
	if _, err := machine.step(commandResult{err: want}); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	machine.state = issueState(99)
	if _, err := machine.step(commandResult{}); err == nil {
		t.Fatal("unknown state returned nil")
	}
}

func TestIssuePlatforms(t *testing.T) {
	if got, want := issuePlatforms("### Platform\r\n\r\nmacOS, Windows, Linux"), []string{"os: mac", "os: windows", "os: linux"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("issuePlatforms() = %v, want %v", got, want)
	}
	if got := issuePlatforms("### Platform\n\n_No response_"); got != nil {
		t.Fatalf("issuePlatforms() = %v, want nil", got)
	}
}
