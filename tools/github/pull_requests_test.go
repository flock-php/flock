package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flock/tools/github/github_issue"
	"flock/tools/github/github_pr"
	"flock/tools/github/issue_reference"
	"flock/tools/github/pr_assignment"
	"flock/tools/github/pr_gate"
)

func TestPullRequestEventWithoutChangesDoesNotPanic(t *testing.T) {
	event, err := github_pr.ParseEvent([]byte(`{"action":"opened","pull_request":{"number":87}}`))
	if err != nil {
		t.Fatal(err)
	}

	if event.BodyEdited {
		t.Fatal("BodyEdited = true, want false")
	}
	if event.BaseEdited {
		t.Fatal("BaseEdited = true, want false")
	}
}

func TestRunPullRequestEventClaimsApprovedIssue(t *testing.T) {
	calls := []string{}
	milestone := 3
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, &milestone)}, calls: &calls}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}}, calls: &calls}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	if issues.assignee != "contributor" || !issues.claimed || !issues.removed {
		t.Fatalf("assignee = %q, claimed = %t, removed = %t", issues.assignee, issues.claimed, issues.removed)
	}
	if pullRequests.comment != "<!-- pr-gate:claim issue=flock#73 author=contributor -->" {
		t.Fatalf("claim comment = %q", pullRequests.comment)
	}
	want := []string{"pr:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim", "pr:find-claim-record", "pr:create-comment", "issue:get", "issue:find-claim", "issue:create-claim", "issue:assign", "issue:add-label", "issue:remove-help-wanted", "pr:get", "pr:list-linked-issues", "pr:transfer-milestone", "issue:ensure-claimed-label", "issue:add-label"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRunPullRequestEventSkipsCrossRepositoryAssignmentAndMilestoneTransfer(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes flock-php/binaries#73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{{Owner: "flock-php", Repository: "binaries", Number: 73}}, calls: &calls}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}}, calls: &calls}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pr:get", "pr:get", "pr:list-files", "pr:list-linked-issues",
		"pr:get", "pr:list-files", "pr:list-linked-issues",
		"pr:get", "pr:list-files", "pr:list-linked-issues", "pr:create-comment", "pr:set-state",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRunPullRequestEventClosesIssueClaimedByAnotherContributor(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, files: []github_pr.File{{Filename: "tools/github/main.go"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	claim := &github_issue.Claim{PullRequestNumber: 86, Author: "other-contributor"}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}}, claimReads: []*github_issue.Claim{claim, claim}, calls: &calls}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim", "pr:find-claim-record", "pr:create-comment", "issue:get", "issue:find-claim", "issue:get", "issue:find-claim", "pr:delete-claim-record", "pr:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !strings.Contains(pullRequests.comment, "Issue flock#73 has already been claimed by @other-contributor") ||
		!strings.Contains(pullRequests.comment, "other issues labeled `help wanted`") {
		t.Fatalf("comment = %q", pullRequests.comment)
	}
	if issues.findClaimCalls != 3 {
		t.Fatalf("FindClaim() calls = %d, want gate, assignment, and release reads", issues.findClaimCalls)
	}
}

func TestRunPullRequestEventDoesNotCloseWhenConflictDisappearsBeforeAssignmentReread(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, files: []github_pr.File{{Filename: "tools/github/main.go"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	issues := &pullRequestIssueFake{
		issue:      &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
		claimReads: []*github_issue.Claim{{PullRequestNumber: 86, Author: "other-contributor"}, nil},
		calls:      &calls,
	}

	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	if pullRequests.pullRequest.State != "open" || strings.Contains(pullRequests.comment, "already been claimed") {
		t.Fatalf("state = %q, comment = %q; want the PR left open after the claim disappears", pullRequests.pullRequest.State, pullRequests.comment)
	}
	if issues.findClaimCalls != 2 {
		t.Fatalf("FindClaim() calls = %d, want gate read and assignment reread", issues.findClaimCalls)
	}
}

func TestRunPullRequestEventIgnoresClaimedLabelForAuthorization(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(fmt.Sprintf("claimed=%t", claimed), func(t *testing.T) {
			calls := []string{}
			labels := []github_issue.Label{}
			if claimed {
				labels = append(labels, github_issue.Label{Name: github_issue.Claimed})
			}
			pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
				Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"}, Labels: labels,
			}, files: []github_pr.File{{Filename: "tools/github/main.go"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
			issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}, calls: &calls}

			if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"synchronize","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
				t.Fatal(err)
			}
			if !github_pr.HasLabel(pullRequests.pullRequest, github_issue.Claimed) {
				t.Fatalf("labels = %+v, want the display label after successful transfer", pullRequests.pullRequest.Labels)
			}
			want := []string{"pr:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim", "pr:find-claim-record", "pr:create-comment", "issue:get", "issue:find-claim", "issue:create-claim", "issue:assign", "issue:add-label", "issue:remove-help-wanted", "pr:get", "pr:list-linked-issues", "issue:ensure-claimed-label", "issue:add-label"}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("calls = %v, want %v", calls, want)
			}
		})
	}
}

func TestRunPullRequestEventAllowsDocsOnlyPullRequestsWithConflictingReferences(t *testing.T) {
	for _, tt := range []struct {
		name  string
		issue *github_issue.Issue
	}{
		{
			name:  "lower-numbered pull request",
			issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		},
		{
			name:  "issue assigned to another contributor",
			issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "other-contributor"}}, Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
				Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
			}, files: []github_pr.File{{Filename: "docs/contributing.md"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
			issues := &pullRequestIssueFake{issue: tt.issue, calls: &calls}

			if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
				t.Fatal(err)
			}
			if pullRequests.pullRequest.State != "open" || pullRequests.comment != "" || issues.assignee != "" {
				t.Fatalf("state = %q, comment = %q, assignee = %q; want docs-only PR unchanged", pullRequests.pullRequest.State, pullRequests.comment, issues.assignee)
			}
		})
	}
}

func TestRunPullRequestEventCompletesClaimAfterAssignmentRetry(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	issues := &pullRequestIssueFake{
		issue:     &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		calls:     &calls,
		assignErr: errors.New("assign"),
	}
	service := newPullRequestEventService(pullRequests, issues)
	event := []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)
	if err := service.Run("flock-php", "flock", event); err == nil {
		t.Fatal("runPullRequestEvent() returned nil error")
	}
	if github_pr.HasLabel(pullRequests.pullRequest, github_issue.Claimed) {
		t.Fatalf("labels after failed assignment = %+v", pullRequests.pullRequest.Labels)
	}
	issues.assignErr = nil
	if err := service.Run("flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	if !github_pr.HasLabel(pullRequests.pullRequest, github_issue.Claimed) {
		t.Fatalf("labels after successful retry = %+v", pullRequests.pullRequest.Labels)
	}
}

func TestRunPullRequestEventRetriesMilestoneTransferBeforeCompletingClaim(t *testing.T) {
	calls := []string{}
	milestone := 3
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, &milestone)}, transferErr: errors.New("transfer"), calls: &calls}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}, calls: &calls}
	service := newPullRequestEventService(pullRequests, issues)
	event := []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)

	if err := service.Run("flock-php", "flock", event); err == nil {
		t.Fatal("Run() returned nil error")
	}
	if github_pr.HasLabel(pullRequests.pullRequest, github_issue.Claimed) {
		t.Fatalf("labels after failed milestone transfer = %+v", pullRequests.pullRequest.Labels)
	}

	pullRequests.transferErr = nil
	pullRequests.transferredIssue = 0
	if err := service.Run("flock-php", "flock", event); err != nil {
		t.Fatal(err)
	}
	if pullRequests.transferredIssue != 73 || !github_pr.HasLabel(pullRequests.pullRequest, github_issue.Claimed) {
		t.Fatalf("transferred issue = %d, labels = %+v; want transfer of #73 and claimed label", pullRequests.transferredIssue, pullRequests.pullRequest.Labels)
	}
	if count := strings.Count(strings.Join(calls, ","), "pr:create-comment"); count != 1 {
		t.Fatalf("claim comments = %d, want 1", count)
	}
}

func TestRunPullRequestEventClosesAssignedIssueForAnotherAuthor(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, files: []github_pr.File{{Filename: "tools/github/main.go"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	issues := &pullRequestIssueFake{
		issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "other"}}, Labels: []github_issue.Label{{Name: "help wanted"}}},
		calls: &calls,
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRunPullRequestEventReevaluatesWhenIssueApprovalIsWithdrawn(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "closed", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, gateClosed: true, files: []github_pr.File{{Filename: "tools/github/main.go"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	issues := &pullRequestIssueFake{
		issue: &github_issue.Issue{Number: 74, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
		issueReads: []*github_issue.Issue{
			{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: "help wanted"}}},
		},
		calls: &calls,
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"edited","changes":{"body":{}},"pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pullRequests.comment, "Flock takes pull requests only for issues a maintainer has approved") {
		t.Fatalf("re-evaluation comment = %q", pullRequests.comment)
	}
	want := []string{"pr:get", "pr:get", "pr:gate-closed", "pr:list-linked-issues", "issue:get", "issue:find-claim", "pr:set-state", "pr:create-comment", "pr:find-claim-record", "pr:create-comment", "issue:get", "issue:get", "issue:find-claim", "pr:delete-claim-record", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if issues.assignee != "" || issues.removed {
		t.Fatalf("withdrawn approval had claim side effects: assignee = %q, removed = %t", issues.assignee, issues.removed)
	}
}

func TestRunPullRequestEventProcessesReplacementApprovedIssue(t *testing.T) {
	calls := []string{}
	milestone := 4
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73\nFixes #74", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil), linkedIssue(74, &milestone)}, calls: &calls}
	issues := &pullRequestIssueFake{
		issue: &github_issue.Issue{Number: 74, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
		issueReads: []*github_issue.Issue{
			{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 74, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 74, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
		},
		calls: &calls,
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pr:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim", "pr:find-claim-record", "pr:create-comment", "issue:get", "issue:get", "issue:find-claim", "pr:delete-claim-record", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:get", "issue:find-claim", "pr:find-claim-record", "pr:create-comment", "issue:get", "issue:find-claim", "issue:create-claim", "issue:assign", "issue:add-label", "issue:remove-help-wanted", "pr:get", "pr:list-linked-issues", "pr:transfer-milestone", "issue:ensure-claimed-label", "issue:add-label",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if issues.assignedIssue != 74 || pullRequests.transferredIssue != 74 {
		t.Fatalf("assigned issue = %d, transferred issue = %d; want 74 for both", issues.assignedIssue, pullRequests.transferredIssue)
	}
}

func TestRunPullRequestEventBoundsApprovalRetries(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	approved := &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}}
	withdrawn := &github_issue.Issue{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: "help wanted"}}}
	issues := &pullRequestIssueFake{
		issue: approved,
		issueReads: []*github_issue.Issue{
			approved, withdrawn, approved, approved, withdrawn, approved, approved, withdrawn, approved, approved, withdrawn, approved,
		},
		calls: &calls,
	}
	err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`))
	if err == nil || !strings.Contains(err.Error(), "issue approval changed repeatedly") {
		t.Fatalf("runPullRequestEvent() error = %v, want bounded-retry error", err)
	}
	if got := strings.Count(strings.Join(calls, ","), "issue:get"); got != 12 {
		t.Fatalf("issue reads = %d, want 12", got)
	}
}

func TestRunPullRequestEventReleasesAnUnlinkedAutomaticClaim(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{
		pullRequest:  &github_pr.PullRequest{Number: 87, State: "open", BaseRef: "main", Body: "Fixes #74", Author: github_issue.User{Login: "contributor", Type: "User"}},
		claimRecord:  &github_pr.ClaimRecord{Issue: issue_reference.Reference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12},
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(74, nil)}, calls: &calls,
	}
	issues := &pullRequestIssueFake{
		issue:      &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}},
		claim:      &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 19},
		assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"}, calls: &calls,
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"edited","changes":{"body":{}},"pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	if issues.claim != nil || len(issues.issue.Assignees) != 0 || !issues.restored || pullRequests.claimRecord != nil {
		t.Fatalf("release state = %+v, record = %+v", issues, pullRequests.claimRecord)
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"edited","changes":{"body":{}},"pull_request":{"number":87}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestRunPullRequestEventReleasesClosedAndRetargetedClaims(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state string
		base  string
		event string
	}{
		{name: "closed", state: "closed", base: "main", event: `{"action":"closed","pull_request":{"number":87}}`},
		{name: "retargeted", state: "open", base: "release", event: `{"action":"edited","changes":{"base":{}},"pull_request":{"number":87}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pullRequests := &pullRequestEventFake{
				pullRequest: &github_pr.PullRequest{Number: 87, State: tt.state, BaseRef: tt.base},
				claimRecord: &github_pr.ClaimRecord{Issue: issue_reference.Reference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12},
			}
			issues := &pullRequestIssueFake{
				issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}},
				claim: &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 19}, assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"},
			}
			if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(tt.event)); err != nil {
				t.Fatal(err)
			}
			if pullRequests.claimRecord != nil || issues.claim != nil || len(issues.issue.Assignees) != 0 || !issues.restored {
				t.Fatalf("release state = %+v, record = %+v", issues, pullRequests.claimRecord)
			}
		})
	}
}

func TestRunPullRequestEventKeepsMergedClaimRecord(t *testing.T) {
	pullRequests := &pullRequestEventFake{
		pullRequest: &github_pr.PullRequest{Number: 87, State: "closed", Merged: true, BaseRef: "main"},
		claimRecord: &github_pr.ClaimRecord{Issue: issue_reference.Reference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12},
	}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open"}}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"closed","pull_request":{"number":87}}`)); err != nil {
		t.Fatal(err)
	}
	if pullRequests.claimRecord == nil || issues.claim != nil || (issues.calls != nil && len(*issues.calls) != 0) {
		t.Fatalf("merged cleanup changed state: record = %+v, issue calls = %v", pullRequests.claimRecord, issues.calls)
	}
}

func TestRunPullRequestEventRetainsRecordWhenCleanupFails(t *testing.T) {
	pullRequests := &pullRequestEventFake{
		pullRequest:  &github_pr.PullRequest{Number: 87, State: "open", BaseRef: "main"},
		claimRecord:  &github_pr.ClaimRecord{Issue: issue_reference.Reference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12},
		linkedIssues: []github_pr.LinkedIssue{}, deleteRecordErr: errors.New("delete record"),
	}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open"}}
	err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"edited","changes":{"body":{}},"pull_request":{"number":87}}`))
	if err == nil || !strings.Contains(err.Error(), "pull request #87") || pullRequests.claimRecord == nil {
		t.Fatalf("error = %v, record = %+v; want PR context and retained record", err, pullRequests.claimRecord)
	}
}

func TestReplacementPullRequestCanClaimReleasedIssue(t *testing.T) {
	issue := &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}}
	firstPullRequest := &pullRequestEventFake{
		pullRequest: &github_pr.PullRequest{Number: 87, State: "closed", BaseRef: "main"},
		claimRecord: &github_pr.ClaimRecord{Issue: issue_reference.Reference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12},
	}
	issues := &pullRequestIssueFake{issue: issue, claim: &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 19}, assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"}}
	if err := newPullRequestEventService(firstPullRequest, issues).Run("flock-php", "flock", []byte(`{"action":"closed","pull_request":{"number":87}}`)); err != nil {
		t.Fatal(err)
	}
	if len(issue.Assignees) != 0 || !github_issue.HasHelpWanted(issue) {
		t.Fatalf("released issue = %+v", issue)
	}

	replacement := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{Number: 88, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "replacement", Type: "User"}}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}}
	issues.issue = issue
	issues.claim = nil
	if err := newPullRequestEventService(replacement, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":88,"user":{"login":"replacement"}}}`)); err != nil {
		t.Fatal(err)
	}
	if issues.assignee != "replacement" {
		t.Fatalf("replacement assignee = %q", issues.assignee)
	}
}

func TestRunPullRequestEventStopsOnErrors(t *testing.T) {
	tests := []struct {
		name         string
		pullRequests *pullRequestEventFake
		issues       *pullRequestIssueFake
		data         string
	}{
		{name: "invalid payload", pullRequests: &pullRequestEventFake{}, issues: &pullRequestIssueFake{}, data: `{`},
		{name: "gate", pullRequests: &pullRequestEventFake{getErr: errors.New("get")}, issues: &pullRequestIssueFake{}, data: `{"action":"opened","pull_request":{"number":87}}`},
		{name: "assignment", pullRequests: &pullRequestEventFake{pullRequest: &github_pr.PullRequest{Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73"}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}}, issues: &pullRequestIssueFake{issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}}, assignErr: errors.New("assign")}, data: `{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := newPullRequestEventService(tt.pullRequests, tt.issues).Run("flock-php", "flock", []byte(tt.data)); err == nil {
				t.Fatal("runPullRequestEvent() returned nil error")
			}
		})
	}
}

func TestWorkflowTransitionSelectsRecordCleanupAndFollowOnAction(t *testing.T) {
	issue := issue_reference.Reference{Repository: "flock", Number: 73}
	gate := pr_gate.Result{ApprovedIssue: &issue}
	for _, tt := range []struct {
		name       string
		assignment pr_assignment.Result
		want       workflowDecision
	}{
		{name: "maintainer assignment", want: workflowDecision{transition: workflowCompleteApproval, record: releaseClaimRecord, claimant: "contributor"}},
		{name: "automatic claim", assignment: pr_assignment.Result{State: pr_assignment.ResultAutomaticClaim}, want: workflowDecision{transition: workflowCompleteApproval, record: keepClaimRecord, claimant: "contributor"}},
		{name: "conflict", assignment: pr_assignment.Result{State: pr_assignment.ResultConflict, Claimant: "other"}, want: workflowDecision{transition: workflowCloseConflict, record: releaseClaimRecord, claimant: "other"}},
		{name: "withdrawn approval", assignment: pr_assignment.Result{State: pr_assignment.ResultApprovalWithdrawn}, want: workflowDecision{transition: workflowRetryApproval, record: releaseClaimRecord, claimant: "contributor"}},
		{name: "no approved issue", assignment: pr_assignment.Result{}, want: workflowDecision{transition: workflowStop, claimant: "contributor"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gateResult := gate
			if tt.name == "no approved issue" {
				gateResult = pr_gate.Result{}
			}
			got, err := workflowTransitionFor(gateResult, tt.assignment, "contributor")
			if err != nil || got != tt.want {
				t.Fatalf("workflowTransitionFor() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWorkflowTransitionRejectsAnUnknownAssignmentState(t *testing.T) {
	issue := issue_reference.Reference{Repository: "flock", Number: 73}
	_, err := workflowTransitionFor(
		pr_gate.Result{ApprovedIssue: &issue},
		pr_assignment.Result{State: pr_assignment.ResultState(99)},
		"contributor",
	)
	if err == nil {
		t.Fatal("workflowTransitionFor() returned nil error")
	}
}

func TestRetryTransitionBoundsApprovalRetries(t *testing.T) {
	if got := retryTransitionFor(false, 0); got != finishWorkflow {
		t.Fatalf("retryTransitionFor(false, 0) = %d, want %d", got, finishWorkflow)
	}
	if got := retryTransitionFor(true, maxApprovalRetries-1); got != retryWorkflow {
		t.Fatalf("retryTransitionFor(true, max-1) = %d, want %d", got, retryWorkflow)
	}
	if got := retryTransitionFor(true, maxApprovalRetries); got != retryLimitReached {
		t.Fatalf("retryTransitionFor(true, max) = %d, want %d", got, retryLimitReached)
	}
}

func TestLinkedIssueTransitionRequiresTheRecordedRepositoryAndIssue(t *testing.T) {
	issue := issue_reference.Reference{Repository: "flock", Number: 73}
	linked := []github_pr.LinkedIssue{linkedIssue(73, nil)}
	if got := linkedIssueTransitionFor(linked, "flock-php", "flock", issue); got != linkedIssuePresent {
		t.Fatalf("linkedIssueTransitionFor() = %d, want %d", got, linkedIssuePresent)
	}
	if got := linkedIssueTransitionFor(linked, "flock-php", "other", issue); got != linkedIssueMissing {
		t.Fatalf("linkedIssueTransitionFor() = %d, want %d", got, linkedIssueMissing)
	}
	if got := linkedIssueTransitionFor(linked, "flock-php", "flock", issue_reference.Reference{Repository: "flock", Number: 74}); got != linkedIssueMissing {
		t.Fatalf("linkedIssueTransitionFor() = %d, want %d", got, linkedIssueMissing)
	}
}

func TestReconciliationSelectsReleaseBeforeStoppingClosedPullRequests(t *testing.T) {
	pullRequests := &pullRequestEventFake{
		pullRequest: &github_pr.PullRequest{Number: 87, State: "closed", BaseRef: "main"},
		claimRecord: &github_pr.ClaimRecord{Issue: issue_reference.Reference{Repository: "flock", Number: 73}, Author: "contributor", CommentID: 12},
	}
	service := newPullRequestEventService(pullRequests, &pullRequestIssueFake{})
	machine := reconciliationMachine{
		service:    service,
		owner:      "flock-php",
		repository: "flock",
		event:      github_pr.Event{Action: "closed", Number: 87},
	}
	decision, err := machine.evaluate()
	if err != nil {
		t.Fatal(err)
	}
	if decision.transition != releaseAndStopReconciliation || decision.record == nil {
		t.Fatalf("reconciliation decision = %+v, want release-and-stop with record", decision)
	}
}

func TestAPIURLUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "https://github.example")
	if got := apiURL(); got != "https://github.example" {
		t.Fatalf("apiURL() = %q", got)
	}
	t.Setenv("GITHUB_API_URL", "")
	if got := apiURL(); got != "https://api.github.com" {
		t.Fatalf("apiURL() = %q", got)
	}
}

type pullRequestEventFake struct {
	pullRequest      *github_pr.PullRequest
	linkedIssues     []github_pr.LinkedIssue
	files            []github_pr.File
	calls            *[]string
	getErr           error
	comment          string
	gateClosed       bool
	claimRecord      *github_pr.ClaimRecord
	claimRecordErr   error
	deleteRecordErr  error
	linkedErr        error
	transferredIssue int
	transferErr      error
}

func newPullRequestEventService(pullRequests *pullRequestEventFake, issues *pullRequestIssueFake) PullRequestEventService {
	issues.pullRequest = pullRequests.pullRequest
	return PullRequestEventService{
		Assignment: pr_assignment.Dependencies{
			Issues: issues,
		},
		Gate: pr_gate.Dependencies{
			Issues:       issues,
			PullRequests: pullRequests,
		},
		Milestones: pullRequests,
	}
}

func (f *pullRequestEventFake) Get(string, string, int) (*github_pr.PullRequest, error) {
	f.record("pr:get")
	return f.pullRequest, f.getErr
}
func (f *pullRequestEventFake) ListFiles(string, string, int) ([]github_pr.File, error) {
	f.record("pr:list-files")
	return f.files, nil
}
func (f *pullRequestEventFake) GateClosed(string, string, int) (bool, error) {
	f.record("pr:gate-closed")
	return f.gateClosed, nil
}
func (f *pullRequestEventFake) FindClaimRecord(string, string, int) (*github_pr.ClaimRecord, error) {
	return f.claimRecord, f.claimRecordErr
}
func (f *pullRequestEventFake) CreateClaimRecord(_ string, _ string, _ int, issue issue_reference.Reference, author string) (*github_pr.ClaimRecord, error) {
	f.record("pr:find-claim-record")
	if f.claimRecordErr != nil {
		return nil, f.claimRecordErr
	}
	if f.claimRecord != nil && f.claimRecord.Issue == issue && f.claimRecord.Author == author {
		return f.claimRecord, nil
	}
	f.claimRecord = &github_pr.ClaimRecord{Issue: issue, Author: author, CommentID: 1}
	f.record("pr:create-comment")
	f.comment = fmt.Sprintf("<!-- pr-gate:claim issue=%s author=%s -->", issue, author)
	return f.claimRecord, nil
}
func (f *pullRequestEventFake) DeleteClaimRecord(string, string, *github_pr.ClaimRecord) error {
	f.record("pr:delete-claim-record")
	if f.deleteRecordErr != nil {
		return f.deleteRecordErr
	}
	f.claimRecord = nil
	return nil
}
func (f *pullRequestEventFake) CreateComment(_ string, _ string, _ int, comment string) error {
	f.record("pr:create-comment")
	f.comment = comment
	return nil
}
func (f *pullRequestEventFake) SetState(_, _ string, _ int, state string) error {
	f.record("pr:set-state")
	f.pullRequest.State = state
	return nil
}
func (f *pullRequestEventFake) ListLinkedIssues(string, string, int) ([]github_pr.LinkedIssue, error) {
	f.record("pr:list-linked-issues")
	return f.linkedIssues, f.linkedErr
}
func (f *pullRequestEventFake) TransferIssueMilestone(_, _ string, _ int, issueNumber int) (bool, error) {
	f.record("pr:transfer-milestone")
	f.transferredIssue = issueNumber
	return false, f.transferErr
}
func (f *pullRequestEventFake) record(call string) {
	if f.calls != nil {
		*f.calls = append(*f.calls, call)
	}
}

func linkedIssue(number int, milestone *int) github_pr.LinkedIssue {
	return github_pr.LinkedIssue{Owner: "flock-php", Repository: "flock", Number: number, MilestoneNumber: milestone}
}

type pullRequestIssueFake struct {
	issue          *github_issue.Issue
	pullRequest    *github_pr.PullRequest
	issueReads     []*github_issue.Issue
	readIndex      int
	claimReads     []*github_issue.Claim
	claimReadIndex int
	findClaimCalls int
	assignee       string
	assignedIssue  int
	removed        bool
	claimed        bool
	claim          *github_issue.Claim
	assignment     *github_issue.Assignment
	restored       bool
	calls          *[]string
	assignErr      error
}

func (f *pullRequestIssueFake) Get(string, string, int) (*github_issue.Issue, error) {
	f.record("issue:get")
	if f.issueReads != nil {
		issue := f.issueReads[f.readIndex]
		if f.readIndex < len(f.issueReads)-1 {
			f.readIndex++
		}
		return issue, nil
	}
	return f.issue, nil
}
func (f *pullRequestIssueFake) AddAssignee(_ string, _ string, number int, username string) (*github_issue.Issue, error) {
	f.record("issue:assign")
	f.assignee = username
	f.assignedIssue = number
	return &github_issue.Issue{Assignees: append(f.issue.Assignees, github_issue.User{Login: username}), Labels: f.issue.Labels}, f.assignErr
}
func (f *pullRequestIssueFake) CreateClaim(_ string, _ string, _ int, pullRequestNumber int, author string) error {
	f.record("issue:create-claim")
	f.claim = &github_issue.Claim{PullRequestNumber: pullRequestNumber, Author: author}
	return nil
}
func (f *pullRequestIssueFake) FindClaim(string, string, int) (*github_issue.Claim, error) {
	f.record("issue:find-claim")
	f.findClaimCalls++
	if f.claimReads != nil {
		claim := f.claimReads[f.claimReadIndex]
		if f.claimReadIndex < len(f.claimReads)-1 {
			f.claimReadIndex++
		}
		return claim, nil
	}
	return f.claim, nil
}
func (f *pullRequestIssueFake) EnsureClaimedLabel(string, string) error {
	f.record("issue:ensure-claimed-label")
	return nil
}
func (f *pullRequestIssueFake) AddLabel(_ string, _ string, number int, label string) error {
	f.record("issue:add-label")
	if f.pullRequest != nil && number == f.pullRequest.Number {
		f.pullRequest.Labels = append(f.pullRequest.Labels, github_issue.Label{Name: label})
		return nil
	}
	if label == github_issue.Claimed {
		f.claimed = true
	}
	return nil
}
func (f *pullRequestIssueFake) RemoveHelpWanted(string, string, *github_issue.Issue) error {
	f.record("issue:remove-help-wanted")
	f.removed = true
	return nil
}
func (f *pullRequestIssueFake) DeleteClaim(string, string, int64) error {
	f.record("issue:delete-claim")
	f.claim = nil
	return nil
}
func (f *pullRequestIssueFake) LatestAssignment(string, string, int, string) (*github_issue.Assignment, error) {
	f.record("issue:latest-assignment")
	return f.assignment, nil
}
func (f *pullRequestIssueFake) RemoveAssignee(string, string, int, string) error {
	f.record("issue:remove-assignee")
	f.issue.Assignees = nil
	return nil
}
func (f *pullRequestIssueFake) RemoveClaimed(string, string, *github_issue.Issue) error {
	f.record("issue:remove-claimed")
	if f.issue != nil {
		labels := f.issue.Labels[:0]
		for _, label := range f.issue.Labels {
			if label.Name != github_issue.Claimed {
				labels = append(labels, label)
			}
		}
		f.issue.Labels = labels
	}
	return nil
}
func (f *pullRequestIssueFake) RestoreHelpWanted(string, string, *github_issue.Issue) error {
	f.record("issue:restore-help-wanted")
	f.restored = true
	if f.issue != nil && f.issue.State == "open" && len(f.issue.Assignees) == 0 {
		f.issue.Labels = append(f.issue.Labels, github_issue.Label{Name: github_issue.HelpWanted})
	}
	return nil
}
func (f *pullRequestIssueFake) record(call string) {
	if f.calls != nil {
		*f.calls = append(*f.calls, call)
	}
}
