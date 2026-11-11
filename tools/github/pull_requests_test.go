package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"flock/tools/github/github_issue"
	"flock/tools/github/github_pr"
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
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:get", "issue:assign", "repository:role", "issue:add-label", "issue:remove-help-wanted", "pr:get", "pr:list-linked-issues", "pr:transfer-milestone", "issue:ensure-claimed-label", "issue:add-label"}
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
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRunPullRequestEventClosesIssueClaimedByAnotherContributor(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	issues := &pullRequestIssueFake{issue: &github_issue.Issue{
		Number: 73, State: "open", Assignees: []github_issue.User{{Login: "other-contributor"}}, Labels: []github_issue.Label{{Name: "help wanted"}},
	}, calls: &calls}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:get", "repository:role", "pr:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !strings.Contains(pullRequests.comment, "Issue flock#73 has already been claimed by @other-contributor") ||
		!strings.Contains(pullRequests.comment, "other issues labeled `help wanted`") {
		t.Fatalf("comment = %q", pullRequests.comment)
	}
}

func TestRunPullRequestEventKeepsClaimedPullRequestOpen(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Author: github_issue.User{Login: "contributor", Type: "User"},
		Labels: []github_issue.Label{{Name: github_issue.Claimed}},
	}, calls: &calls}
	if err := newPullRequestEventService(pullRequests, &pullRequestIssueFake{calls: &calls}).Run("flock-php", "flock", []byte(`{"action":"synchronize","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
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
}

func TestRunPullRequestEventClosesAssignedIssueForAnotherAuthor(t *testing.T) {
	calls := []string{}
	pullRequests := &pullRequestEventFake{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "main", Body: "Fixes #73", Author: github_issue.User{Login: "contributor", Type: "User"},
	}, linkedIssues: []github_pr.LinkedIssue{linkedIssue(73, nil)}, calls: &calls}
	issues := &pullRequestIssueFake{
		issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "other"}}, Labels: []github_issue.Label{{Name: "help wanted"}}},
		calls: &calls,
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:get", "repository:role", "pr:get", "pr:create-comment", "pr:set-state"}
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
	want := []string{"pr:get", "pr:gate-closed", "pr:list-linked-issues", "issue:get", "pr:set-state", "pr:create-comment", "issue:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:create-comment", "pr:set-state"}
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
			{Number: 74, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
			{Number: 74, State: "open", Labels: []github_issue.Label{{Name: "help wanted"}}},
		},
		calls: &calls,
	}
	if err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`)); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:get",
		"issue:get", "issue:assign", "repository:role", "issue:add-label", "issue:remove-help-wanted", "pr:get", "pr:list-linked-issues", "pr:transfer-milestone", "issue:ensure-claimed-label", "issue:add-label",
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
			approved, withdrawn, approved, withdrawn, approved, withdrawn, approved, withdrawn,
		},
		calls: &calls,
	}
	err := newPullRequestEventService(pullRequests, issues).Run("flock-php", "flock", []byte(`{"action":"opened","pull_request":{"number":87,"user":{"login":"contributor"}}}`))
	if err == nil || !strings.Contains(err.Error(), "issue approval changed repeatedly") {
		t.Fatalf("runPullRequestEvent() error = %v, want bounded-retry error", err)
	}
	if got := strings.Count(strings.Join(calls, ","), "issue:get"); got != 8 {
		t.Fatalf("issue reads = %d, want 8", got)
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
	transferredIssue int
	transferErr      error
}

func newPullRequestEventService(pullRequests *pullRequestEventFake, issues *pullRequestIssueFake) PullRequestEventService {
	issues.pullRequest = pullRequests.pullRequest
	return PullRequestEventService{
		Assignment: pr_assignment.Dependencies{
			Issues:       issues,
			Repositories: issues,
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
	return f.linkedIssues, nil
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
	issue         *github_issue.Issue
	pullRequest   *github_pr.PullRequest
	issueReads    []*github_issue.Issue
	readIndex     int
	assignee      string
	assignedIssue int
	removed       bool
	claimed       bool
	calls         *[]string
	assignErr     error
	role          string
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
func (f *pullRequestIssueFake) EnsureClaimedLabel(string, string) error {
	f.record("issue:ensure-claimed-label")
	return nil
}
func (f *pullRequestIssueFake) RemoveAssignee(_ string, _ string, _ int, username string) error {
	f.record("issue:remove-assignee")
	f.assignee = username
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
func (f *pullRequestIssueFake) record(call string) {
	if f.calls != nil {
		*f.calls = append(*f.calls, call)
	}
}
func (f *pullRequestIssueFake) GetUserRole(string, string, string) (string, error) {
	f.record("repository:role")
	return f.role, nil
}
