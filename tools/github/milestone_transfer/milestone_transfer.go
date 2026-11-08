// Package milestone_transfer moves a referenced issue milestone to its pull request.
package milestone_transfer

import (
	"flock/tools/github/github_service"
	"flock/tools/github/issue_reference"
	"github.com/google/go-github/v93/github"
)

type Service interface {
	PullRequest(owner, repository string, number int) (*github.PullRequest, error)
	LinkedIssues(owner, repository string, pullRequestNumber int) ([]github_service.LinkedIssue, error)
	TransferMilestone(owner, repository string, pullRequestNumber, issueNumber int) (bool, error)
}

func Run(api Service, owner, repository string, pullRequestNumber int, approvedIssue issue_reference.Reference) error {
	if approvedIssue.Repository != repository {
		return nil
	}
	pullRequest, err := api.PullRequest(owner, repository, pullRequestNumber)
	if err != nil || pullRequest.GetState() != "open" {
		return err
	}
	issues, err := api.LinkedIssues(owner, repository, pullRequestNumber)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if issue.Number == approvedIssue.Number && issue.MilestoneNumber != nil {
			_, err := api.TransferMilestone(owner, repository, pullRequestNumber, approvedIssue.Number)
			return err
		}
	}
	return nil
}
