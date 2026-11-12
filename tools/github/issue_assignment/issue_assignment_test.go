package issue_assignment

import (
	"errors"
	"slices"
	"testing"

	"flock/tools/github/github_issue"
)

func TestRunRemovesHelpWantedFromAssignedIssue(t *testing.T) {
	for _, assignee := range []string{"contributor", "maintainer"} {
		t.Run(assignee, func(t *testing.T) {
			issue := &github_issue.Issue{
				Number:    73,
				Assignees: []github_issue.User{{Login: assignee}},
				Labels:    []github_issue.Label{{Name: github_issue.HelpWanted}},
			}
			api := &fakeService{currentIssue: issue}

			if err := Execute(api, "flock-php", "flock", issue); err != nil {
				t.Fatal(err)
			}
			if !api.helpWantedRemoved {
				t.Fatal("help wanted label was not removed")
			}
			if api.claimedRemoved {
				t.Fatal("claimed label was removed without a superseded claim")
			}
		})
	}
}

func TestRunSkipsUnassignedIssuesAndMissingLabels(t *testing.T) {
	tests := []struct {
		name          string
		issue         *github_issue.Issue
		wantClaimRead bool
	}{
		{name: "unassigned", issue: &github_issue.Issue{Number: 73, Labels: []github_issue.Label{{Name: github_issue.HelpWanted}}}},
		{name: "assigned without label", issue: &github_issue.Issue{Number: 73, Assignees: []github_issue.User{{Login: "contributor"}}}, wantClaimRead: true},
		{name: "missing event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &fakeService{currentIssue: tt.issue}
			if err := Execute(api, "flock-php", "flock", tt.issue); err != nil {
				t.Fatal(err)
			}
			if api.helpWantedRemoved {
				t.Fatal("help wanted label was removed")
			}
			if api.claimRead != tt.wantClaimRead {
				t.Fatalf("claim read = %t, want %t", api.claimRead, tt.wantClaimRead)
			}
		})
	}
}

func TestRunUsesCurrentIssueInsteadOfWebhookSnapshot(t *testing.T) {
	eventIssue := &github_issue.Issue{
		Number:    73,
		Assignees: []github_issue.User{{Login: "contributor"}},
		Labels:    []github_issue.Label{{Name: github_issue.HelpWanted}},
	}
	currentIssue := &github_issue.Issue{
		Number: 73,
		Labels: []github_issue.Label{{Name: github_issue.HelpWanted}},
	}
	api := &fakeService{currentIssue: currentIssue}
	if err := Execute(api, "flock-php", "flock", eventIssue); err != nil {
		t.Fatal(err)
	}
	if api.helpWantedRemoved || api.claimRead {
		t.Fatal("help wanted label was removed from an issue that is currently unassigned")
	}
}

func TestRunRemovesClaimedAfterHelpWantedWhenAnAdditionalAssigneeSupersedesTheClaim(t *testing.T) {
	issue := &github_issue.Issue{
		Number:    73,
		Assignees: []github_issue.User{{Login: "contributor"}, {Login: "maintainer"}},
		Labels:    []github_issue.Label{{Name: github_issue.HelpWanted}, {Name: github_issue.Claimed}},
	}
	api := &fakeService{currentIssue: issue, claim: &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"}}

	if err := Execute(api, "flock-php", "flock", issue); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(api.calls, []string{"find-claim", "remove-help-wanted", "remove-claimed"}) {
		t.Fatalf("calls = %v", api.calls)
	}
	if !api.helpWantedRemoved || !api.claimedRemoved {
		t.Fatalf("help wanted removed = %t, claimed removed = %t", api.helpWantedRemoved, api.claimedRemoved)
	}
}

func TestRunStopsCleanupAfterAnError(t *testing.T) {
	findErr := errors.New("find claim")
	helpErr := errors.New("remove help wanted")
	claimedErr := errors.New("remove claimed")
	tests := []struct {
		name       string
		findErr    error
		helpErr    error
		claimedErr error
		wantCalls  []string
		wantErr    error
	}{
		{name: "marker read", findErr: findErr, wantCalls: []string{"find-claim"}, wantErr: findErr},
		{name: "help wanted removal", helpErr: helpErr, wantCalls: []string{"find-claim", "remove-help-wanted"}, wantErr: helpErr},
		{name: "claimed removal", claimedErr: claimedErr, wantCalls: []string{"find-claim", "remove-help-wanted", "remove-claimed"}, wantErr: claimedErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := &github_issue.Issue{
				Number:    73,
				Assignees: []github_issue.User{{Login: "contributor"}, {Login: "maintainer"}},
				Labels:    []github_issue.Label{{Name: github_issue.HelpWanted}, {Name: github_issue.Claimed}},
			}
			api := &fakeService{
				currentIssue: issue,
				claim:        &github_issue.Claim{PullRequestNumber: 87, Author: "contributor"},
				findClaimErr: tt.findErr,
				helpErr:      tt.helpErr,
				claimedErr:   tt.claimedErr,
			}

			if err := Execute(api, "flock-php", "flock", issue); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Execute() error = %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(api.calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", api.calls, tt.wantCalls)
			}
		})
	}
}

type fakeService struct {
	currentIssue      *github_issue.Issue
	claim             *github_issue.Claim
	findClaimErr      error
	helpErr           error
	claimedErr        error
	claimRead         bool
	helpWantedRemoved bool
	claimedRemoved    bool
	calls             []string
}

func (f *fakeService) Get(string, string, int) (*github_issue.Issue, error) {
	return f.currentIssue, nil
}

func (f *fakeService) FindClaim(string, string, int) (*github_issue.Claim, error) {
	f.calls = append(f.calls, "find-claim")
	f.claimRead = true
	return f.claim, f.findClaimErr
}

func (f *fakeService) RemoveHelpWanted(_ string, _ string, issue *github_issue.Issue) error {
	f.calls = append(f.calls, "remove-help-wanted")
	if github_issue.HasHelpWanted(issue) {
		f.helpWantedRemoved = true
	}
	return f.helpErr
}

func (f *fakeService) RemoveClaimed(_ string, _ string, issue *github_issue.Issue) error {
	f.calls = append(f.calls, "remove-claimed")
	if github_issue.HasLabel(issue, github_issue.Claimed) {
		f.claimedRemoved = true
	}
	return f.claimedErr
}
