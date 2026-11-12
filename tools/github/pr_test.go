package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPRMachineRunsAnAutomaticClaimThroughMilestoneAndLabelCompletion(t *testing.T) {
	event := pullRequestEvent{Action: "opened", Number: 87}
	machine := newPRMachine("flock-php", "flock", event)
	pr := &pullRequest{Number: 87, State: "open", BaseRef: "main", Author: user{Login: "contributor", Type: "User"}}
	approved := &issue{Number: 73, State: "open", Labels: []label{{Name: helpWantedLabel}}}
	withAssignee := &issue{Number: 73, State: "open", Assignees: []user{{Login: "contributor"}}, Labels: []label{{Name: helpWantedLabel}}}
	milestone := 3

	steps := []struct {
		result commandResult
		kind   commandKind
	}{
		{commandResult{}, commandReadPullRequest},
		{commandResult{pullRequest: pr}, commandReadClaimRecord},
		{commandResult{}, commandReadPullRequest},
		{commandResult{pullRequest: pr}, commandListPullRequestFiles},
		{commandResult{files: []file{{Filename: "internal/site.go"}}}, commandListLinkedIssues},
		{commandResult{linkedIssues: []linkedIssue{{Owner: "flock-php", Repository: "flock", Number: 73}}}, commandReadIssue},
		{commandResult{issue: approved}, commandReadIssueClaim},
		{commandResult{}, commandReadClaimRecord},
		{commandResult{}, commandCreateClaimRecord},
		{commandResult{claimRecord: &claimRecord{Issue: issueReference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12}}, commandReadIssue},
		{commandResult{issue: approved}, commandReadIssueClaim},
		{commandResult{}, commandCreateIssueClaim},
		{commandResult{}, commandAddIssueAssignee},
		{commandResult{issue: withAssignee}, commandAddIssueLabel},
		{commandResult{}, commandRemoveIssueLabel},
		{commandResult{}, commandReadIssueMilestone},
		{commandResult{issue: approved, milestone: &milestone}, commandSetPullRequestMilestone},
		{commandResult{}, commandRemoveIssueMilestone},
		{commandResult{}, commandEnsureClaimedLabel},
		{commandResult{}, commandAddIssueLabel},
		{commandResult{}, commandDone},
	}

	for index, step := range steps {
		command, err := machine.step(step.result)
		if err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		if command.kind != step.kind {
			t.Fatalf("step %d command = %d, want %d", index, command.kind, step.kind)
		}
	}
}

func TestPRMachineRetriesOnlyMilestoneRemoval(t *testing.T) {
	machine := newPRMachine("flock-php", "flock", pullRequestEvent{Number: 87})
	machine.state = prStateMilestoneRemove
	machine.snapshot.removals = 1

	command, err := machine.step(commandResult{err: errors.New("remove")})
	if err != nil || command.kind != commandWait || command.delay != 3*time.Second {
		t.Fatalf("first cleanup failure = %+v, %v", command, err)
	}
	if machine.snapshot.removals != 2 {
		t.Fatalf("removal attempts = %d, want 2", machine.snapshot.removals)
	}

	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandRemoveIssueMilestone {
		t.Fatalf("retry command = %+v, %v", command, err)
	}

	command, err = machine.step(commandResult{err: errors.New("second remove")})
	if err != nil || command.kind != commandWait || command.delay != 3*time.Second {
		t.Fatalf("second cleanup failure = %+v, %v", command, err)
	}
	if machine.snapshot.removals != 3 {
		t.Fatalf("removal attempts = %d, want 3", machine.snapshot.removals)
	}

	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandRemoveIssueMilestone {
		t.Fatalf("second retry command = %+v, %v", command, err)
	}

	want := errors.New("final remove")
	if _, err := machine.step(commandResult{err: want}); !errors.Is(err, want) {
		t.Fatalf("third cleanup error = %v, want %v", err, want)
	}
}

func TestPRMachineReleasesClaimRecordWhenItsPullRequestNoLongerQualifies(t *testing.T) {
	record := &claimRecord{Issue: issueReference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 9}
	tests := []struct {
		name          string
		event         pullRequestEvent
		pullRequest   *pullRequest
		linked        []linkedIssue
		needsLinkRead bool
		want          commandKind
	}{
		{
			name:        "closed",
			event:       pullRequestEvent{Action: "closed", Number: 87},
			pullRequest: &pullRequest{Number: 87, State: "closed", BaseRef: "main"},
			want:        commandReadIssue,
		},
		{
			name:        "merged",
			event:       pullRequestEvent{Action: "closed", Number: 87},
			pullRequest: &pullRequest{Number: 87, State: "closed", Merged: true, BaseRef: "main"},
			want:        commandDone,
		},
		{
			name:        "changed base",
			event:       pullRequestEvent{Action: "edited", Number: 87, BaseEdited: true},
			pullRequest: &pullRequest{Number: 87, State: "open", BaseRef: "release"},
			want:        commandReadIssue,
		},
		{
			name:          "removed closing reference",
			event:         pullRequestEvent{Action: "edited", Number: 87},
			pullRequest:   &pullRequest{Number: 87, State: "open", BaseRef: "main"},
			linked:        []linkedIssue{{Owner: "flock-php", Repository: "flock", Number: 74}},
			needsLinkRead: true,
			want:          commandReadIssue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := newPRMachine("flock-php", "flock", tt.event)
			if command, err := machine.step(commandResult{}); err != nil || command.kind != commandReadPullRequest {
				t.Fatalf("initial command = %+v, %v", command, err)
			}
			if command, err := machine.step(commandResult{pullRequest: tt.pullRequest}); err != nil || command.kind != commandReadClaimRecord {
				t.Fatalf("claim record command = %+v, %v", command, err)
			}
			command, err := machine.step(commandResult{claimRecord: record})
			if err != nil {
				t.Fatal(err)
			}
			if tt.needsLinkRead {
				if command.kind != commandListLinkedIssues {
					t.Fatalf("link command = %+v", command)
				}
				command, err = machine.step(commandResult{linkedIssues: tt.linked})
				if err != nil {
					t.Fatal(err)
				}
			}
			if command.kind != tt.want {
				t.Fatalf("final command = %+v, want %d", command, tt.want)
			}
			if tt.want == commandReadIssue && command.issue != record.Issue {
				t.Fatalf("release issue = %s, want %s", command.issue, record.Issue)
			}
		})
	}
}

func TestPRMachineAddsPullRequestClaimedOnlyAfterMilestoneCleanup(t *testing.T) {
	machine := newPRMachine("flock-php", "flock", pullRequestEvent{Number: 87})
	machine.state = prStateMilestoneRead
	milestone := 3
	machine.snapshot.approved = issueReference{Repository: "flock", Number: 73}
	machine.snapshot.issue = &issue{Number: 73}

	command, err := machine.step(commandResult{issue: machine.snapshot.issue, milestone: &milestone})
	if err != nil || command.kind != commandSetPullRequestMilestone {
		t.Fatalf("milestone set command = %+v, %v", command, err)
	}
	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandRemoveIssueMilestone {
		t.Fatalf("milestone removal command = %+v, %v", command, err)
	}

	removals := 1
	waits := 0
	for removals < 3 {
		command, err = machine.step(commandResult{err: errors.New("remove")})
		if err != nil || command.kind != commandWait || command.delay != 3*time.Second {
			t.Fatalf("cleanup retry %d = %+v, %v", removals, command, err)
		}
		waits++
		command, err = machine.step(commandResult{})
		if err != nil || command.kind != commandRemoveIssueMilestone {
			t.Fatalf("cleanup retry command %d = %+v, %v", removals+1, command, err)
		}
		removals++
	}
	if waits != 2 || removals != 3 {
		t.Fatalf("cleanup sequence = %d removals, %d waits; want 3 removals and 2 waits", removals, waits)
	}
	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandEnsureClaimedLabel {
		t.Fatalf("completion label command = %+v, %v", command, err)
	}
	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandAddIssueLabel || command.pullRequestNumber != 87 {
		t.Fatalf("pull request label command = %+v, %v", command, err)
	}
}

func TestPRMachinePreservesMaintainerOwnedAndAmbiguousReleaseState(t *testing.T) {
	tests := []struct {
		name       string
		assignees  []user
		assignment *assignment
	}{
		{name: "maintainer assignment", assignees: []user{{Login: "contributor"}}, assignment: &assignment{Assigner: "maintainer"}},
		{name: "additional assignee", assignees: []user{{Login: "contributor"}, {Login: "maintainer"}}, assignment: &assignment{Assigner: "github-actions[bot]"}},
		{name: "missing provenance", assignees: []user{{Login: "contributor"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := newPRMachine("flock-php", "flock", pullRequestEvent{Number: 87})
			machine.state = prStateReleaseClaim
			machine.snapshot.release = &claimRecord{Issue: issueReference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 9}
			machine.snapshot.issue = &issue{Number: 73, State: "open", Assignees: tt.assignees, Labels: []label{{Name: claimedLabel}}}
			machine.snapshot.claim = &claim{PullRequestNumber: 87, Author: "contributor", CommentID: 10}

			command, err := machine.step(commandResult{claim: machine.snapshot.claim})
			if err != nil || command.kind != commandDeleteIssueClaim {
				t.Fatalf("claim cleanup command = %+v, %v", command, err)
			}
			if tt.name != "additional assignee" {
				command, err = machine.step(commandResult{})
				if err != nil || command.kind != commandReadLatestAssignment {
					t.Fatalf("assignment read command = %+v, %v", command, err)
				}
			}
			command, err = machine.step(commandResult{assignment: tt.assignment})
			if err != nil || command.kind != commandDeleteClaimRecord {
				t.Fatalf("release record command = %+v, %v", command, err)
			}
		})
	}
}

func TestPRMachineReleasesMaintainerClaimRecordBeforeMilestoneTransfer(t *testing.T) {
	machine := newPRMachine("flock-php", "flock", pullRequestEvent{Number: 87})
	machine.state = prStateAssignClaim
	machine.snapshot.pullRequest = &pullRequest{Number: 87, Author: user{Login: "contributor"}}
	machine.snapshot.approved = issueReference{Repository: "flock", Number: 73}
	machine.snapshot.record = &claimRecord{Issue: machine.snapshot.approved, Author: "contributor", CommentID: 9}
	machine.snapshot.issue = &issue{Number: 73, State: "open", Assignees: []user{{Login: "contributor"}}}

	command, err := machine.step(commandResult{})
	if err != nil || command.kind != commandReadIssue {
		t.Fatalf("release issue read command = %+v, %v", command, err)
	}
	command, err = machine.step(commandResult{issue: machine.snapshot.issue})
	if err != nil || command.kind != commandReadIssueClaim {
		t.Fatalf("release claim read command = %+v, %v", command, err)
	}
	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandDeleteClaimRecord {
		t.Fatalf("maintainer record deletion command = %+v, %v", command, err)
	}
	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandReadIssueMilestone {
		t.Fatalf("milestone read command after release = %+v, %v", command, err)
	}
}

func TestPRMachineDeletesRecordAfterAutomaticReleaseCleanup(t *testing.T) {
	machine := newPRMachine("flock-php", "flock", pullRequestEvent{Number: 87})
	machine.state = prStateReleaseClaim
	machine.snapshot.release = &claimRecord{Issue: issueReference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 9}
	machine.snapshot.issue = &issue{Number: 73, State: "open", Assignees: []user{{Login: "contributor"}}, Labels: []label{{Name: claimedLabel}}}
	machine.snapshot.claim = &claim{PullRequestNumber: 87, Author: "contributor", CommentID: 10}

	commands := []commandKind{
		commandDeleteIssueClaim,
		commandReadLatestAssignment,
		commandRemoveIssueAssignee,
		commandRemoveIssueLabel,
		commandRestoreHelpWanted,
		commandDeleteClaimRecord,
		commandDone,
	}
	results := []commandResult{
		{claim: machine.snapshot.claim},
		{},
		{assignment: &assignment{Assigner: "github-actions[bot]"}},
		{},
		{},
		{},
		{},
	}
	for index, want := range commands {
		command, err := machine.step(results[index])
		if err != nil || command.kind != want {
			t.Fatalf("step %d command = %+v, %v; want %d", index, command, err, want)
		}
	}
}

func TestRunPRMilestoneRemovalFailurePreservesAutomaticClaimState(t *testing.T) {
	var requests []string
	removals := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/pulls/87":
			_, _ = io.WriteString(w, `{"number":87,"state":"open","base":{"ref":"main"},"user":{"login":"contributor","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/flock-php/flock/issues/87/comments", "GET /repos/flock-php/flock/issues/73/comments":
			_, _ = io.WriteString(w, `[]`)
		case "GET /repos/flock-php/flock/pulls/87/files":
			_, _ = io.WriteString(w, `[{"filename":"internal/site.go"}]`)
		case "POST /graphql":
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":73,"milestone":{"number":3},"repository":{"name":"flock","owner":{"login":"flock-php"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"state":"open","milestone":{"number":3},"labels":[{"name":"help wanted"}],"assignees":[]}`)
		case "POST /repos/flock-php/flock/issues/87/comments", "POST /repos/flock-php/flock/issues/73/comments":
			_, _ = io.WriteString(w, `{"id":101}`)
		case "POST /repos/flock-php/flock/issues/73/assignees":
			_, _ = io.WriteString(w, `{"number":73,"state":"open","labels":[{"name":"help wanted"}],"assignees":[{"login":"contributor"}]}`)
		case "POST /repos/flock-php/flock/issues/73/labels", "POST /repos/flock-php/flock/issues/87/labels":
			_, _ = io.WriteString(w, `[]`)
		case "DELETE /repos/flock-php/flock/issues/73/labels/help wanted":
			_, _ = io.WriteString(w, `{}`)
		case "PATCH /repos/flock-php/flock/issues/73":
			removals++
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"remove milestone"}`)
		case "PATCH /repos/flock-php/flock/issues/87":
			_, _ = io.WriteString(w, `{}`)
		case "GET /repos/flock-php/flock/labels/claimed":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		case "POST /repos/flock-php/flock/labels":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	adapter, err := newGitHubAdapter(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	event := []byte(`{"action":"opened","pull_request":{"number":87,"state":"open","base":{"ref":"main"},"user":{"login":"contributor","type":"User"},"author_association":"CONTRIBUTOR"}}`)
	err = runPR("flock-php", "flock", event, adapter)
	if err == nil {
		t.Fatal("runPR returned nil after exhausted milestone cleanup")
	}
	for _, want := range []string{
		"state " + stateName(prStateMilestoneRemove),
		"command remove issue milestone",
		"pull request #87",
		"issue flock#73",
		"remove milestone",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want context %q", err, want)
		}
	}
	if removals != 3 {
		t.Fatalf("milestone removals = %d, want 3", removals)
	}
	if !containsRequest(requests, "POST /repos/flock-php/flock/issues/87/comments") || !containsRequest(requests, "POST /repos/flock-php/flock/issues/73/comments") {
		t.Fatalf("claim records were not both created: %v", requests)
	}
	if containsRequest(requests, "DELETE /repos/flock-php/flock/issues/comments/101") {
		t.Fatalf("automatic claim record was deleted after cleanup failure: %v", requests)
	}
	if containsRequest(requests, "POST /repos/flock-php/flock/issues/87/labels") {
		t.Fatalf("pull request claimed label was added before cleanup succeeded: %v", requests)
	}
}

func containsRequest(requests []string, want string) bool {
	for _, request := range requests {
		if request == want {
			return true
		}
	}
	return false
}
