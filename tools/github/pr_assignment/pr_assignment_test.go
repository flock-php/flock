package pr_assignment

import (
	"errors"
	"reflect"
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
	for _, api := range []*fakeService{
		{getErr: errors.New("get")},
		{issue: issue, findClaimErr: errors.New("find claim")},
		{issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}}, findClaimErr: errors.New("find assigned claim")},
		{issue: issue, createClaimErr: errors.New("create claim")},
		{issue: issue, assignErr: errors.New("assign")},
		{issue: issue, labelErr: errors.New("label")},
		{issue: issue, removeErr: errors.New("remove")},
	} {
		if _, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err == nil {
			t.Fatal("Execute() returned nil error")
		}
	}
}

func TestExecuteWithdrawsApprovalForAnUnassignedAuthor(t *testing.T) {
	api := &fakeService{issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "another-contributor"}}, Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}}
	result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor")
	if err != nil || result.State != ResultApprovalWithdrawn {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
	if api.assignee != "" || api.removed || api.claim != nil {
		t.Fatalf("unexpected claim side effects: %+v", api)
	}
	if api.findClaimCalls != 0 {
		t.Fatalf("FindClaim() calls = %d, want 0", api.findClaimCalls)
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
	if err != nil || result.State != ResultConflict || result.Claimant != "other-contributor" {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
	if api.removed || api.assignee != "" {
		t.Fatalf("removed = %t, assignee = %q", api.removed, api.assignee)
	}
}

func TestExecuteCompletesTheOwnerRetry(t *testing.T) {
	api := &fakeService{
		issue: &github_issue.Issue{Number: 73, State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}},
		claim: &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"},
	}
	result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor")
	if err != nil || result.State != ResultAutomaticClaim || api.assignee != "contributor" || !api.claimed || !api.removed {
		t.Fatalf("result = %+v, claimed = %t, removed = %t, err = %v", result, api.claimed, api.removed, err)
	}
}

func TestExecuteReadsButDoesNotApplyAStaleClaimForAnAssignedAuthor(t *testing.T) {
	api := &fakeService{
		issue: &github_issue.Issue{
			Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}},
			Labels: []github_issue.Label{{Name: github_issue.HelpWanted}},
		},
		claim: &github_issue.Claim{PullRequestNumber: 86, Author: "former-contributor"},
	}

	result, err := Execute(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor")
	if err != nil || result != (Result{}) {
		t.Fatalf("Execute() = %+v, %v; want no-op", result, err)
	}
	if api.findClaimCalls != 1 || api.assignee != "" || api.claimed || api.removed {
		t.Fatalf("unexpected stale-claim side effects: %+v", api)
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
		if err != nil || result.State != ResultApprovalWithdrawn {
			t.Fatalf("Execute() = %+v, %v; want withdrawn approval", result, err)
		}
		if api.assignee != "" || api.removed || api.claim != nil {
			t.Fatalf("unexpected claim side effects: %+v", api)
		}
	}
}

func TestReleaseRemovesOnlyBotOwnedAutomaticState(t *testing.T) {
	api := &fakeService{
		issue:      &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}},
		claim:      &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 9},
		assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"},
	}
	if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil {
		t.Fatal(err)
	}
	if !api.deleted || !api.removedAssignee || !api.removedClaimed || !api.restored {
		t.Fatalf("release state = %+v", api)
	}
	wantCalls := []string{"get", "find-claim", "delete-claim", "latest-assignment", "remove-assignee", "remove-claimed", "restore-help-wanted"}
	if !reflect.DeepEqual(api.calls, wantCalls) {
		t.Fatalf("release calls = %v, want %v", api.calls, wantCalls)
	}
}

func TestReleasePreservesMaintainerOwnedState(t *testing.T) {
	for _, tt := range []struct {
		name       string
		assignees  []github_issue.User
		assignment *github_issue.Assignment
	}{
		{name: "maintainer assignment", assignees: []github_issue.User{{Login: "contributor"}}, assignment: &github_issue.Assignment{Assigner: "maintainer"}},
		{name: "additional assignee", assignees: []github_issue.User{{Login: "contributor"}, {Login: "maintainer"}}, assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"}},
		{name: "missing provenance", assignees: []github_issue.User{{Login: "contributor"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeService{issue: &github_issue.Issue{Number: 73, State: "open", Assignees: tt.assignees}, claim: &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 9}, assignment: tt.assignment}
			if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil {
				t.Fatal(err)
			}
			if !api.deleted || api.removedAssignee || api.removedClaimed || api.restored {
				t.Fatalf("release state = %+v", api)
			}
		})
	}
}

func TestReleaseRemovesClosedIssueOwnershipWithoutRestoringAvailability(t *testing.T) {
	api := &fakeService{
		issue:      &github_issue.Issue{Number: 73, State: "closed", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}},
		claim:      &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 9},
		assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"},
	}
	if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil {
		t.Fatal(err)
	}
	if !api.removedAssignee || !api.removedClaimed || api.restored {
		t.Fatalf("release state = %+v", api)
	}
}

func TestReleaseIgnoresNonmatchingClaim(t *testing.T) {
	api := &fakeService{issue: &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}}, claim: &github_issue.Claim{PullRequestNumber: 86, Author: "other"}, assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"}}
	if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil {
		t.Fatal(err)
	}
	if api.deleted || api.removedAssignee {
		t.Fatalf("release state = %+v", api)
	}
}

func TestReleaseReturnsEveryCleanupError(t *testing.T) {
	for _, tt := range []struct {
		name string
		set  func(*fakeService)
	}{
		{name: "get", set: func(api *fakeService) { api.getErr = errors.New("get") }},
		{name: "find claim", set: func(api *fakeService) { api.findClaimErr = errors.New("find claim") }},
		{name: "delete claim", set: func(api *fakeService) { api.deleteClaimErr = errors.New("delete claim") }},
		{name: "timeline", set: func(api *fakeService) { api.assignmentErr = errors.New("timeline") }},
		{name: "remove assignee", set: func(api *fakeService) { api.removeAssigneeErr = errors.New("remove assignee") }},
		{name: "remove claimed", set: func(api *fakeService) { api.removeClaimedErr = errors.New("remove claimed") }},
		{name: "restore help wanted", set: func(api *fakeService) { api.restoreErr = errors.New("restore help wanted") }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeService{
				issue:      &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}},
				claim:      &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 9},
				assignment: &github_issue.Assignment{Assigner: "github-actions[bot]"},
			}
			tt.set(api)
			if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err == nil {
				t.Fatal("Release() returned nil error")
			}
		})
	}
}

func TestReleaseRetriesAfterAssigneeRemoval(t *testing.T) {
	api := &fakeService{
		issue:            &github_issue.Issue{Number: 73, State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}},
		claim:            &github_issue.Claim{PullRequestNumber: 87, Author: "contributor", CommentID: 9},
		assignment:       &github_issue.Assignment{Assigner: "github-actions[bot]"},
		removeClaimedErr: errors.New("remove claimed"),
	}
	if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err == nil {
		t.Fatal("Release() returned nil error")
	}
	api.removeClaimedErr = nil
	if err := Release(Dependencies{Issues: api}, "flock-php", issue_reference.Reference{Repository: "flock", Number: 73}, 87, "contributor"); err != nil {
		t.Fatal(err)
	}
	if !api.restored {
		t.Fatalf("retry did not restore availability: %+v", api)
	}
}

func TestReleaseDecisionForClaimSelectsCleanupDirection(t *testing.T) {
	issue := &github_issue.Issue{State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}}
	claim := &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"}
	for _, tt := range []struct {
		name        string
		issue       *github_issue.Issue
		claim       *github_issue.Claim
		pullRequest int
		author      string
		want        releaseTransition
		actions     []releaseAction
	}{
		{name: "different owner", issue: issue, claim: claim, pullRequest: 88, author: "contributor", want: releaseNoOp},
		{name: "additional assignee", issue: &github_issue.Issue{State: "open", Assignees: []github_issue.User{{Login: "contributor"}, {Login: "maintainer"}}}, claim: claim, pullRequest: 87, author: "contributor", want: releaseDeleteClaim, actions: []releaseAction{deleteIssueClaim}},
		{name: "automatic state", issue: issue, claim: claim, pullRequest: 87, author: "contributor", want: releaseAutomaticState, actions: []releaseAction{deleteIssueClaim}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := releaseDecisionForClaim(tt.issue, tt.claim, tt.pullRequest, tt.author)
			if got.transition != tt.want || !reflect.DeepEqual(got.actions, tt.actions) {
				t.Fatalf("releaseDecisionForClaim() = %+v, want transition %d actions %v", got, tt.want, tt.actions)
			}
		})
	}
}

func TestReleaseDecisionForProvenanceSelectsOrderedAutomaticCleanup(t *testing.T) {
	issue := &github_issue.Issue{State: "open", Assignees: []github_issue.User{{Login: "contributor"}}, Labels: []github_issue.Label{{Name: github_issue.Claimed}}}
	decision := releaseDecisionForProvenance(issue, &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"}, "contributor", &Assignment{Assigner: "github-actions[bot]"})
	want := []releaseAction{removeAutomaticAssignee, removeAutomaticClaimedLabel, restoreAutomaticHelpWanted}
	if decision.transition != releaseAutomaticState || !reflect.DeepEqual(decision.actions, want) {
		t.Fatalf("releaseDecisionForProvenance() = %+v, want transition %d actions %v", decision, releaseAutomaticState, want)
	}
}

type fakeService struct {
	issue             *github_issue.Issue
	assignedIssue     *github_issue.Issue
	claim             *github_issue.Claim
	assignee          string
	removed           bool
	claimed           bool
	getErr            error
	findClaimErr      error
	createClaimErr    error
	assignErr         error
	removeErr         error
	labelErr          error
	findClaimCalls    int
	assignment        *github_issue.Assignment
	deleteClaimErr    error
	assignmentErr     error
	removeAssigneeErr error
	removeClaimedErr  error
	restoreErr        error
	deleted           bool
	removedAssignee   bool
	removedClaimed    bool
	restored          bool
	calls             []string
}

func (f *fakeService) Get(string, string, int) (*github_issue.Issue, error) {
	f.calls = append(f.calls, "get")
	return f.issue, f.getErr
}
func (f *fakeService) FindClaim(string, string, int) (*github_issue.Claim, error) {
	f.calls = append(f.calls, "find-claim")
	f.findClaimCalls++
	return f.claim, f.findClaimErr
}
func (f *fakeService) CreateClaim(_ string, _ string, _ int, pullRequestNumber int, author string) error {
	if f.createClaimErr != nil {
		return f.createClaimErr
	}
	f.claim = &github_issue.Claim{PullRequestNumber: pullRequestNumber, Author: author}
	return nil
}
func (f *fakeService) DeleteClaim(string, string, int64) error {
	f.calls = append(f.calls, "delete-claim")
	f.deleted = true
	f.claim = nil
	return f.deleteClaimErr
}
func (f *fakeService) LatestAssignment(string, string, int, string) (*github_issue.Assignment, error) {
	f.calls = append(f.calls, "latest-assignment")
	return f.assignment, f.assignmentErr
}
func (f *fakeService) RemoveAssignee(string, string, int, string) error {
	f.calls = append(f.calls, "remove-assignee")
	if f.removeAssigneeErr != nil {
		return f.removeAssigneeErr
	}
	f.removedAssignee = true
	f.issue.Assignees = nil
	return nil
}
func (f *fakeService) RemoveClaimed(string, string, *github_issue.Issue) error {
	f.calls = append(f.calls, "remove-claimed")
	if f.removeClaimedErr != nil {
		return f.removeClaimedErr
	}
	f.removedClaimed = true
	return nil
}
func (f *fakeService) RestoreHelpWanted(string, string, *github_issue.Issue) error {
	f.calls = append(f.calls, "restore-help-wanted")
	if f.restoreErr != nil {
		return f.restoreErr
	}
	if f.issue != nil && f.issue.State == "open" && len(f.issue.Assignees) == 0 {
		f.restored = true
	}
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
