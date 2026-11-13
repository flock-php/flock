package milestone_transfer

import (
	"testing"

	"flock/tools/github/github_service"
	"flock/tools/github/issue_reference"
	"github.com/google/go-github/v93/github"
)

func TestRunUsesFirstUserLinkedIssueWithAMilestone(t *testing.T) {
	milestone := 3
	api := &fakeService{
		pullRequest: pullRequest("open"),
		issues: []github_service.LinkedIssue{
			{Number: 72, MilestoneNumber: &milestone},
			{Number: 73, MilestoneNumber: &milestone},
		},
	}
	if err := Run(api, "flock-php", "flock", 87, issue_reference.Reference{Repository: "flock", Number: 73}); err != nil {
		t.Fatal(err)
	}
	if api.issueNumber != 73 {
		t.Fatalf("issue number = %d, want 73", api.issueNumber)
	}
}

func TestRunDoesNotTransferWithoutAMilestonedUserLinkedIssue(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("open"), issues: []github_service.LinkedIssue{{Number: 73}}}
	if err := Run(api, "flock-php", "flock", 87, issue_reference.Reference{Repository: "flock", Number: 73}); err != nil {
		t.Fatal(err)
	}
	if api.issueNumber != 0 {
		t.Fatalf("issue number = %d, want none", api.issueNumber)
	}
}

func TestRunSkipsClosedPullRequests(t *testing.T) {
	api := &fakeService{pullRequest: pullRequest("closed")}
	if err := Run(api, "flock-php", "flock", 87, issue_reference.Reference{Repository: "flock", Number: 73}); err != nil {
		t.Fatal(err)
	}
	if api.issueNumber != 0 {
		t.Fatalf("issue number = %d, want none", api.issueNumber)
	}
}

type fakeService struct {
	pullRequest *github.PullRequest
	issues      []github_service.LinkedIssue
	issueNumber int
}

func (f *fakeService) PullRequest(string, string, int) (*github.PullRequest, error) {
	return f.pullRequest, nil
}
func (f *fakeService) LinkedIssues(string, string, int) ([]github_service.LinkedIssue, error) {
	return f.issues, nil
}
func (f *fakeService) TransferMilestone(_ string, _ string, _ int, issueNumber int) (bool, error) {
	f.issueNumber = issueNumber
	return true, nil
}

func pullRequest(state string) *github.PullRequest {
	return &github.PullRequest{State: new(state)}
}
