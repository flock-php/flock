package pr_assignment

import (
	"errors"
	"testing"

	"flock/tools/github/github_issue"
	"flock/tools/github/issue_reference"
)

func TestRunAssignsAuthorAndMarksIssueClaimed(t *testing.T) {
	api := &fakeService{issue: &github_issue.Issue{
		Number: 73, State: "open",
		Labels: []github_issue.Label{{Name: github_issue.HelpWanted}},
	}}

	if _, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil {
		t.Fatal(err)
	}
	if api.assignee != "contributor" || !api.claimed || !api.removed {
		t.Fatalf("assignee = %q, claimed = %t, removed = %t", api.assignee, api.claimed, api.removed)
	}
}

func TestExecuteReturnsServiceErrors(t *testing.T) {
	issue := &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}
	for _, api := range []*fakeService{{getErr: errors.New("get")}, {issue: issue, findClaimErr: errors.New("find claim")}, {issue: issue, createClaimErr: errors.New("create claim")}, {issue: issue, assignErr: errors.New("assign")}, {issue: issue, labelErr: errors.New("label")}, {issue: issue, removeErr: errors.New("remove")}} {
		if _, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err == nil {
			t.Fatal("Execute() returned nil error")
		}
	}
}

func TestExecuteWithdrawsApprovalForAnUnassignedAuthor(t *testing.T) {
	api := &fakeService{issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "another-contributor"}}, Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}}
	result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor")
	if err != nil || !result.ApprovalWithdrawn {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
	if api.assignee != "" || api.removed || api.claim != nil {
		t.Fatalf("unexpected claim side effects: %+v", api)
	}
}

func TestExecutePreservesMaintainerAssignments(t *testing.T) {
	api := &fakeService{issue: &github_issue.Issue{
		Number:    73,
		State:     "open",
		Assignees: []github_issue.User{{Login: "contributor"}, {Login: "another-contributor"}},
		Labels:    []github_issue.Label{{Name: github_issue.HelpWanted}},
	}}
	if result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil || result != (Result{}) {
		t.Fatal(err)
	}
	if api.assignee != "" || api.claimed || api.removed || api.claim != nil {
		t.Fatalf("unexpected automatic claim state: %+v", api)
	}
}

func TestExecuteFailsWhenGitHubIgnoresAssignment(t *testing.T) {
	api := &fakeService{
		issue: &github_issue.Issue{
			Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}},
		},
		assignedIssue: &github_issue.Issue{
			Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}},
		},
	}

	if _, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err == nil {
		t.Fatal("Execute() returned nil error")
	}
	if api.claim == nil || api.claimed || api.removed {
		t.Fatalf("claim = %+v, claimed = %t, removed = %t", api.claim, api.claimed, api.removed)
	}
}

func TestExecuteReportsAnotherPullRequestClaim(t *testing.T) {
	api := &fakeService{
		issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		claim: &github_issue.Claim{PullRequestNumber: 86, Author: "other-contributor"},
	}
	result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "zulu")
	if err != nil || result.ClaimedBy != "other-contributor" {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
	if api.removed || api.assignee != "" {
		t.Fatalf("removed = %t, assignee = %q", api.removed, api.assignee)
	}
}

func TestExecuteCompletesTheOwnerRetry(t *testing.T) {
	api := &fakeService{
		issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		claim: &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"},
	}
	result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor")
	if err != nil || !result.AutomatedClaim || !api.claimed || !api.removed {
		t.Fatalf("result = %+v, claimed = %t, removed = %t, err = %v", result, api.claimed, api.removed, err)
	}
}

func TestExecuteReportsWithdrawnApprovalWithoutClaiming(t *testing.T) {
	for _, issue := range []*github_issue.Issue{
		nil,
		{Number: 73, State: "closed", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		{Number: 73, State: "open"},
	} {
		api := &fakeService{issue: issue}
		result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor")
		if err != nil || !result.ApprovalWithdrawn {
			t.Fatalf("Execute() = %+v, %v; want withdrawn approval", result, err)
		}
		if api.assignee != "" || api.removed || api.claim != nil {
			t.Fatalf("unexpected claim side effects: %+v", api)
		}
	}
}

type fakeService struct {
	issue          *github_issue.Issue
	assignedIssue  *github_issue.Issue
	claim          *github_issue.Claim
	assignee       string
	removed        bool
	claimed        bool
	getErr         error
	findClaimErr   error
	createClaimErr error
	assignErr      error
	removeErr      error
	labelErr       error
}

func (f *fakeService) Get(string, string, int) (*github_issue.Issue, error) { return f.issue, f.getErr }
func (f *fakeService) FindClaim(string, string, int) (*github_issue.Claim, error) {
	return f.claim, f.findClaimErr
}
func (f *fakeService) CreateClaim(_ string, _ string, _ int, pullRequestNumber int, author string) error {
	if f.createClaimErr != nil {
		return f.createClaimErr
	}
	f.claim = &github_issue.Claim{PullRequestNumber: pullRequestNumber, Author: author}
	return nil
}
func (f *fakeService) AddAssignee(_ string, _ string, _ int, username string) (*github_issue.Issue, error) {
	f.assignee = username
	if f.assignErr != nil {
		return nil, f.assignErr
	}
	if f.assignedIssue != nil {
		return f.assignedIssue, nil
	}
	return &github_issue.Issue{Number: f.issue.Number, State: f.issue.State, Assignees: append(f.issue.Assignees, github_issue.User{Login: username}), Labels: f.issue.Labels}, nil
}
func (f *fakeService) AddLabel(_ string, _ string, _ int, label string) error {
	if label == github_issue.Claimed {
		f.claimed = true
	}
	return f.labelErr
}
func (f *fakeService) RemoveHelpWanted(string, string, *github_issue.Issue) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = true
	return nil
}
