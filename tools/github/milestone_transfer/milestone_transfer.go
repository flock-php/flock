// Package milestone_transfer moves a referenced issue milestone to its pull request.
package milestone_transfer

import (
	"flock/tools/github/github_pr"
	"flock/tools/github/issue_reference"
)

type PullRequestMilestoneService interface {
	Get(owner, repository string, number int) (*github_pr.PullRequest, error)
	ListLinkedIssues(owner, repository string, pullRequestNumber int) ([]github_pr.LinkedIssue, error)
	TransferIssueMilestone(owner, repository string, pullRequestNumber, issueNumber int) (bool, error)
}

func Execute(pullRequests PullRequestMilestoneService, owner, repository string, pullRequestNumber int, approvedIssue issue_reference.Reference) error {
	if approvedIssue.Repository != repository {
		return nil
	}
	pullRequest, err := pullRequests.Get(owner, repository, pullRequestNumber)
	if err != nil {
		return err
	}
	if pullRequest == nil || pullRequest.State != "open" {
		return nil
	}
	issues, err := pullRequests.ListLinkedIssues(owner, repository, pullRequestNumber)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if issue.Number == approvedIssue.Number && issue.MilestoneNumber != nil {
			_, err := pullRequests.TransferIssueMilestone(owner, repository, pullRequestNumber, approvedIssue.Number)
			return err
		}
	}
	return nil
}
