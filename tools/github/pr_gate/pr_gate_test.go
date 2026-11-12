package pr_gate

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"flock/tools/github/github_issue"
	"flock/tools/github/github_pr"
	"github.com/google/go-github/v93/github"
)

func TestRunReopensOnlyGateClosedPullRequests(t *testing.T) {
	tests := []struct {
		name       string
		gateClosed bool
		wantState  string
	}{
		{name: "reopens gate-closed pull request", gateClosed: true, wantState: "open"},
		{name: "does not reopen manually closed pull request", gateClosed: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeService{
				pullRequest: pullRequest("closed", "Fixes #73"),
				gateClosed:  tt.gateClosed,
				issue:       approvedIssue(),
			}
			result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "edited", Number: 87, BodyEdited: true})
			if err != nil {
				t.Fatal(err)
			}
			if api.state != tt.wantState {
				t.Fatalf("state = %q, want %q", api.state, tt.wantState)
			}
			if tt.gateClosed && (result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73) {
				t.Fatalf("approved issue = %+v, want flock#73", result.ApprovedIssue)
			}
			if tt.gateClosed {
				want := []string{"pr:get", "pr:gate-closed", "pr:list-linked-issues", "issue:get", "issue:find-claim", "pr:set-state", "pr:create-comment"}
				if !reflect.DeepEqual(api.calls, want) {
					t.Fatalf("calls = %v, want %v", api.calls, want)
				}
			}
		})
	}
}

func TestRunClosesUnapprovedPullRequest(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", "Fixes #73"), files: []github_pr.File{{Filename: "internal/site.go"}}, issue: &github_issue.Issue{State: "open"}}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.state != "closed" || !api.commented {
		t.Fatalf("state = %q, commented = %t", api.state, api.commented)
	}
}

func TestRunChecksFilesBeforeApprovingIssue(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", "Fixes #73"), issue: approvedIssue()}
	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "opened", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("approved issue = %+v, want flock#73", result.ApprovedIssue)
	}
	if api.filesCalls != 1 {
		t.Fatalf("Files() calls = %d, want 1", api.filesCalls)
	}
}

func TestRunSkipsMissingIssueReferences(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", "Fixes #72 and #73"), issue: approvedIssue()}
	api.linkedIssues = []github_pr.LinkedIssue{linkedIssue(72), linkedIssue(73)}
	issues := &fakeIssueService{
		api: api,
		errors: map[int]error{
			72: &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}},
		},
	}

	result, err := Execute(Dependencies{Issues: issues, PullRequests: api}, "flock-php", "flock", Event{Action: "opened", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("approved issue = %+v, want flock#73", result.ApprovedIssue)
	}
	if api.filesCalls != 1 {
		t.Fatalf("Files() calls = %d, want 1", api.filesCalls)
	}
}

func TestRunClosesPullRequestsWithOnlyMissingIssueReferences(t *testing.T) {
	api := &fakeService{
		pullRequest:  pullRequest("open", "Fixes #72"),
		files:        []github_pr.File{{Filename: "internal/site.go"}},
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(72)},
	}
	issues := &fakeIssueService{
		api: api,
		errors: map[int]error{
			72: &github.ErrorResponse{Response: &http.Response{StatusCode: http.StatusNotFound}},
		},
	}

	if _, err := Execute(Dependencies{Issues: issues, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.state != "closed" || !api.commented {
		t.Fatalf("state = %q, commented = %t", api.state, api.commented)
	}
}

func TestRunAllowsDocsOnlyPullRequestsWithoutAnApprovedIssue(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", ""),
		files:       []github_pr.File{{Filename: "docs/contributing.md"}},
	}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "opened", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.filesCalls != 1 {
		t.Fatalf("Files() calls = %d, want 1", api.filesCalls)
	}
	if api.state != "" || api.commented {
		t.Fatalf("state = %q, commented = %t", api.state, api.commented)
	}
}

func TestRunBypassesMaintainersWithoutCheckingIssuesOrFiles(t *testing.T) {
	pr := pullRequest("open", "")
	pr.AuthorAssociation = "MEMBER"
	api := &fakeService{pullRequest: pr}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "opened", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.issueCalls != 0 || api.filesCalls != 0 {
		t.Fatalf("Issue() calls = %d, Files() calls = %d, want 0", api.issueCalls, api.filesCalls)
	}
}

func TestRunBypassesClaimedPullRequestsWithoutCheckingIssuesOrFiles(t *testing.T) {
	pr := pullRequest("open", "Fixes #73")
	pr.Labels = []github_issue.Label{{Name: github_issue.Claimed}}
	api := &fakeService{pullRequest: pr}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.issueCalls != 0 || api.filesCalls != 0 || len(api.calls) != 1 {
		t.Fatalf("calls = %v, want only Get()", api.calls)
	}
}

func TestRunReopensClaimedPullRequestClosedForTheWrongBase(t *testing.T) {
	pr := pullRequest("closed", "Fixes #73")
	pr.Labels = []github_issue.Label{{Name: github_issue.Claimed}}
	api := &fakeService{pullRequest: pr, gateClosed: true, issue: approvedIssue()}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "edited", Number: 87, BodyEdited: true})
	if err != nil {
		t.Fatal(err)
	}
	if api.state != "open" || result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("state = %q, result = %+v; want open and approved issue flock#73", api.state, result)
	}
}

func TestRunReturnsMaintainerAssignedIssueForAnAssignedContributor(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", "Fixes #73"),
		issue: &github_issue.Issue{
			State: "open", Assignees: []github_issue.User{{Login: "contributor"}, {Login: "another-contributor"}}, Labels: []github_issue.Label{{Name: github_issue.HelpWanted}},
		},
		findClaimErr: errors.New("manual assignments must not read claims"),
	}
	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("result = %+v, want approved issue flock#73", result)
	}
	if want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get"}; !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunSelectsAnApprovedLinkedIssueAfterAnEarlierConflict(t *testing.T) {
	api := &fakeService{
		pullRequest:  pullRequest("open", "Fixes #73 and #74"),
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(73), linkedIssue(74)},
		issues: map[int]*github_issue.Issue{
			73: approvedIssue(),
			74: approvedIssue(),
		},
		claims: map[int]*github_issue.Claim{
			73: {PullRequestNumber: 86, Author: "first-contributor"},
		},
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 74 || result.ClaimedBy != "" {
		t.Fatalf("result = %+v, want approved issue flock#74 without a conflict", result)
	}
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim", "issue:get", "issue:find-claim"}
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunStopsAtAnApprovedLinkedIssueBeforeLaterConflicts(t *testing.T) {
	api := &fakeService{
		pullRequest:  pullRequest("open", "Fixes #73 and #74"),
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(73), linkedIssue(74)},
		issues: map[int]*github_issue.Issue{
			73: approvedIssue(),
			74: approvedIssue(),
		},
		claims: map[int]*github_issue.Claim{
			74: {PullRequestNumber: 86, Author: "later-contributor"},
		},
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 || result.ClaimedBy != "" {
		t.Fatalf("result = %+v, want approved issue flock#73 without a conflict", result)
	}
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim"}
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunReturnsTheFirstConflictAfterScanningAllLinkedIssues(t *testing.T) {
	api := &fakeService{
		pullRequest:  pullRequest("open", "Fixes #73 and #74"),
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(73), linkedIssue(74)},
		issues: map[int]*github_issue.Issue{
			73: approvedIssue(),
			74: approvedIssue(),
		},
		claims: map[int]*github_issue.Claim{
			73: {PullRequestNumber: 86, Author: "first-contributor"},
			74: {PullRequestNumber: 85, Author: "second-contributor"},
		},
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 || result.ClaimedBy != "first-contributor" {
		t.Fatalf("result = %+v, want the first conflict on flock#73", result)
	}
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim", "issue:get", "issue:find-claim"}
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunReturnsClaimOwnerForAnotherPullRequest(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", "Fixes #73"),
		issue:       approvedIssue(),
		claim:       &github_issue.Claim{PullRequestNumber: 86, Author: "first-contributor"},
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 || result.ClaimedBy != "first-contributor" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunAllowsTheClaimOwnerToRetry(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", "Fixes #73"),
		issue:       approvedIssue(),
		claim:       &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"},
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ClaimedBy != "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunApprovesFirstProcessedPullRequestDespiteALowerNumber(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", "Fixes #73"),
		issue:       approvedIssue(),
	}
	api.pullRequest.Number = 87
	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("result = %+v, want approved issue flock#73", result)
	}
	if want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "issue:find-claim"}; !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunUsesClosingReferencesInsteadOfPlainMentions(t *testing.T) {
	api := &fakeService{
		pullRequest:  pullRequest("open", "Unlike #136, this fixes #150"),
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(150)},
		issues: map[int]*github_issue.Issue{
			136: approvedIssue(),
			150: approvedIssue(),
		},
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 150 {
		t.Fatalf("result = %+v, want approved issue flock#150", result)
	}
}

func TestRunUsesDevelopmentSidebarLinks(t *testing.T) {
	api := &fakeService{
		pullRequest:  pullRequest("open", "Implements the requested behavior."),
		linkedIssues: []github_pr.LinkedIssue{linkedIssue(73)},
		issue:        approvedIssue(),
	}

	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || result.ApprovedIssue.Number != 73 {
		t.Fatalf("result = %+v, want approved issue flock#73", result)
	}
}

func TestRunIgnoresClosingReferencesOutsideTheCurrentRepository(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("open", "Fixes other-owner/flock#73"),
		linkedIssues: []github_pr.LinkedIssue{
			{Owner: "other-owner", Repository: "flock", Number: 73},
			{Owner: "flock-php", Repository: "binaries", Number: 74},
		},
		files: []github_pr.File{{Filename: "internal/site.go"}},
		issue: approvedIssue(),
	}

	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.state != "closed" || api.issueCalls != 0 {
		t.Fatalf("state = %q, issue reads = %d; want closed without issue reads", api.state, api.issueCalls)
	}
}

func TestRunClosesWrongBaseWithCommentBeforeClosing(t *testing.T) {
	api := &fakeService{pullRequest: &github_pr.PullRequest{
		Number: 87, State: "open", BaseRef: "release", Author: github_issue.User{Login: "contributor", Type: "User"},
	}}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "opened", Number: 87}); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunClosesClaimedPullRequestsRetargetedAwayFromMain(t *testing.T) {
	pr := pullRequest("open", "Fixes #73")
	pr.BaseRef = "release"
	pr.Labels = []github_issue.Label{{Name: github_issue.Claimed}}
	api := &fakeService{pullRequest: pr}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "edited", Number: 87, BaseEdited: true}); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunReopensGateClosedPullRequestWithAnApprovedIssue(t *testing.T) {
	api := &fakeService{
		pullRequest: pullRequest("closed", "Fixes #73"),
		gateClosed:  true,
		issue:       approvedIssue(),
	}
	result, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "edited", Number: 87, BodyEdited: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.ApprovedIssue == nil || api.state != "open" {
		t.Fatalf("result = %+v, state = %q; want approved and open", result, api.state)
	}
}

func TestRunRetriesNewUnapprovedPullRequestsBeforeClosing(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", ""), files: []github_pr.File{{Filename: "internal/site.go"}}}
	if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", Event{Action: "opened", Number: 87}); err != nil {
		t.Fatal(err)
	}
	if api.getCalls != 3 {
		t.Fatalf("Get() calls = %d, want 3", api.getCalls)
	}
	want := []string{"pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:get", "pr:list-files", "pr:list-linked-issues", "issue:get", "pr:create-comment", "pr:set-state"}
	if !reflect.DeepEqual(api.calls, want) {
		t.Fatalf("calls = %v, want %v", api.calls, want)
	}
}

func TestRunSkipsIneligibleClosedAndEditedPullRequests(t *testing.T) {
	for _, tt := range []struct {
		name  string
		pr    *github_pr.PullRequest
		event Event
	}{
		{name: "body unchanged", pr: pullRequest("closed", "Fixes #73"), event: Event{Action: "edited", Number: 87}},
		{name: "merged", pr: &github_pr.PullRequest{Number: 87, State: "closed", Merged: true, BaseRef: "main"}, event: Event{Action: "edited", Number: 87, BodyEdited: true}},
		{name: "wrong base", pr: &github_pr.PullRequest{Number: 87, State: "closed", BaseRef: "release"}, event: Event{Action: "edited", Number: 87, BodyEdited: true}},
		{name: "edited body only", pr: pullRequest("open", ""), event: Event{Action: "edited", Number: 87, BodyEdited: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeService{pullRequest: tt.pr}
			if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: api}, PullRequests: api}, "flock-php", "flock", tt.event); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(api.calls, []string{"pr:get"}) {
				t.Fatalf("calls = %v, want only Get()", api.calls)
			}
		})
	}
}

func TestRunReturnsPullRequestServiceErrors(t *testing.T) {
	tests := []struct {
		name  string
		api   *fakeService
		event Event
	}{
		{name: "get", api: &fakeService{getErr: errors.New("get")}, event: Event{Action: "opened", Number: 87}},
		{name: "gate closed", api: &fakeService{pullRequest: pullRequest("closed", ""), gateErr: errors.New("gate")}, event: Event{Action: "edited", Number: 87, BodyEdited: true}},
		{name: "list files", api: &fakeService{pullRequest: pullRequest("open", ""), filesErr: errors.New("files")}, event: Event{Action: "synchronize", Number: 87}},
		{name: "comment", api: &fakeService{pullRequest: &github_pr.PullRequest{Number: 87, State: "open", BaseRef: "release"}, commentErr: errors.New("comment")}, event: Event{Action: "opened", Number: 87}},
		{name: "set state", api: &fakeService{pullRequest: &github_pr.PullRequest{Number: 87, State: "open", BaseRef: "release"}, stateErr: errors.New("state")}, event: Event{Action: "opened", Number: 87}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Execute(Dependencies{Issues: &fakeIssueService{api: tt.api}, PullRequests: tt.api}, "flock-php", "flock", tt.event); err == nil {
				t.Fatal("Execute() returned nil error")
			}
		})
	}
}

func TestRunReturnsIssueServiceErrors(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open", "Fixes #73")}
	issues := &fakeIssueService{api: api, err: errors.New("issue")}
	if _, err := Execute(Dependencies{Issues: issues, PullRequests: api}, "flock-php", "flock", Event{Action: "synchronize", Number: 87}); err == nil {
		t.Fatal("Execute() returned nil error")
	}
	if api.state != "" || api.commented {
		t.Fatalf("state = %q, commented = %t; want no gate action", api.state, api.commented)
	}
}

func TestApplyRejectsUnknownTransition(t *testing.T) {
	api := &fakeService{}
	gate := pullRequestGate{dependencies: Dependencies{PullRequests: api}}
	if _, _, err := gate.apply(gateTransition{state: gateTransitionState(99)}); err == nil {
		t.Fatal("apply() returned nil error")
	}
	if len(api.calls) != 0 {
		t.Fatalf("calls = %v, want no side effects", api.calls)
	}
}

type fakeService struct {
	pullRequest  *github_pr.PullRequest
	files        []github_pr.File
	linkedIssues []github_pr.LinkedIssue
	issue        *github_issue.Issue
	issues       map[int]*github_issue.Issue
	claim        *github_issue.Claim
	claims       map[int]*github_issue.Claim
	gateClosed   bool
	state        string
	commented    bool
	filesCalls   int
	issueCalls   int
	getCalls     int
	calls        []string
	getErr       error
	filesErr     error
	linkedErr    error
	gateErr      error
	findClaimErr error
	commentErr   error
	stateErr     error
}

func (f *fakeService) Get(string, string, int) (*github_pr.PullRequest, error) {
	f.getCalls++
	f.calls = append(f.calls, "pr:get")
	return f.pullRequest, f.getErr
}
func (f *fakeService) ListFiles(string, string, int) ([]github_pr.File, error) {
	f.filesCalls++
	f.calls = append(f.calls, "pr:list-files")
	return f.files, f.filesErr
}
func (f *fakeService) ListLinkedIssues(string, string, int) ([]github_pr.LinkedIssue, error) {
	f.calls = append(f.calls, "pr:list-linked-issues")
	if f.linkedIssues == nil {
		return []github_pr.LinkedIssue{linkedIssue(73)}, f.linkedErr
	}
	return f.linkedIssues, f.linkedErr
}
func (f *fakeService) GateClosed(string, string, int) (bool, error) {
	f.calls = append(f.calls, "pr:gate-closed")
	return f.gateClosed, f.gateErr
}
func (f *fakeService) ClaimCommentExists(string, string, int) (bool, error) {
	f.calls = append(f.calls, "pr:claim-comment-exists")
	return false, nil
}
func (f *fakeService) CreateComment(string, string, int, string) error {
	f.calls = append(f.calls, "pr:create-comment")
	f.commented = true
	return f.commentErr
}
func (f *fakeService) SetState(_ string, _ string, _ int, state string) error {
	f.calls = append(f.calls, "pr:set-state")
	f.state = state
	return f.stateErr
}

type fakeIssueService struct {
	api    *fakeService
	err    error
	errors map[int]error
}

func (f *fakeIssueService) Get(_ string, _ string, number int) (*github_issue.Issue, error) {
	f.api.issueCalls++
	f.api.calls = append(f.api.calls, "issue:get")
	if err := f.errors[number]; err != nil {
		return nil, err
	}
	if issue, ok := f.api.issues[number]; ok {
		return issue, f.err
	}
	return f.api.issue, f.err
}
func (f *fakeIssueService) FindClaim(_ string, _ string, number int) (*github_issue.Claim, error) {
	f.api.calls = append(f.api.calls, "issue:find-claim")
	if f.api.claims != nil {
		return f.api.claims[number], f.api.findClaimErr
	}
	return f.api.claim, f.api.findClaimErr
}
func (f *fakeIssueService) EnsureClaimedLabel(string, string) error {
	f.api.calls = append(f.api.calls, "issue:ensure-claimed-label")
	return nil
}
func (f *fakeIssueService) AddLabel(string, string, int, string) error {
	f.api.calls = append(f.api.calls, "issue:add-label")
	return nil
}
func pullRequest(state, body string) *github_pr.PullRequest {
	return &github_pr.PullRequest{
		Number:  87,
		State:   state,
		BaseRef: "main",
		Body:    body,
		Author:  github_issue.User{Login: "contributor", Type: "User"},
	}
}

func approvedIssue() *github_issue.Issue {
	return &github_issue.Issue{State: "open", Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}
}

func linkedIssue(number int) github_pr.LinkedIssue {
	return github_pr.LinkedIssue{Owner: "flock-php", Repository: "flock", Number: number}
}
