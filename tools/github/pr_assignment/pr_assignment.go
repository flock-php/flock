// Package pr_assignment claims approved issues for pull request authors.
package pr_assignment

import (
	"fmt"

	"flock/tools/github/github_issue"
	"flock/tools/github/issue_reference"
)

type IssueClaimer interface {
	Get(owner, repository string, number int) (*github_issue.Issue, error)
	AddAssignee(owner, repository string, number int, username string) (*github_issue.Issue, error)
	AddLabel(owner, repository string, number int, label string) error
	RemoveHelpWanted(owner, repository string, issue *github_issue.Issue) error
	CreateClaim(owner, repository string, number, pullRequestNumber int, author string) error
	FindClaim(owner, repository string, number int) (*github_issue.Claim, error)
}

type Dependencies struct {
	Issues IssueClaimer
}

type Result struct {
	ClaimedBy         string
	ApprovalWithdrawn bool
	AutomatedClaim    bool
}

func Execute(dependencies Dependencies, owner string, issueReference issue_reference.Reference, pullRequestNumber int, author string) (Result, error) {
	claim := issueClaim{
		dependencies:      dependencies,
		owner:             owner,
		issueReference:    issueReference,
		pullRequestNumber: pullRequestNumber,
		author:            author,
	}
	return claim.evaluate()
}

type issueClaim struct {
	dependencies      Dependencies
	owner             string
	issueReference    issue_reference.Reference
	pullRequestNumber int
	author            string
}

func (c issueClaim) evaluate() (Result, error) {
	issue, err := c.dependencies.Issues.Get(c.owner, c.issueReference.Repository, c.issueReference.Number)
	if err != nil {
		return Result{}, err
	}
	switch github_issue.ApprovalFor(issue, c.author) {
	case github_issue.ApprovalDenied:
		return Result{ApprovalWithdrawn: true}, nil
	case github_issue.ApprovalByAssignment:
		return Result{}, nil
	case github_issue.ApprovalRequiresAutomaticClaim:
		claim, err := c.dependencies.Issues.FindClaim(c.owner, c.issueReference.Repository, c.issueReference.Number)
		if err != nil {
			return Result{}, err
		}
		if claim != nil {
			if claim.PullRequestNumber != c.pullRequestNumber || claim.Author != c.author {
				return Result{ClaimedBy: claim.Author}, nil
			}
			return c.assignClaimOwner(issue, true)
		}
		if err := c.dependencies.Issues.CreateClaim(c.owner, c.issueReference.Repository, c.issueReference.Number, c.pullRequestNumber, c.author); err != nil {
			return Result{}, err
		}
		return c.assignClaimOwner(issue, true)
	default:
		return Result{}, fmt.Errorf("unknown issue approval state")
	}
}

func (c issueClaim) assignClaimOwner(issue *github_issue.Issue, automatedClaim bool) (Result, error) {
	if !github_issue.HasAssignee(issue, c.author) {
		var err error
		issue, err = c.dependencies.Issues.AddAssignee(c.owner, c.issueReference.Repository, c.issueReference.Number, c.author)
		if err != nil {
			return Result{}, err
		}
	}
	if !github_issue.HasAssignee(issue, c.author) {
		return Result{}, fmt.Errorf("assign issue %s to @%s: GitHub did not add the assignee", c.issueReference, c.author)
	}
	if err := markClaimed(c.dependencies.Issues, c.owner, c.issueReference.Repository, issue); err != nil {
		return Result{}, err
	}
	return Result{AutomatedClaim: automatedClaim}, nil
}

func markClaimed(issues IssueClaimer, owner, repository string, issue *github_issue.Issue) error {
	if err := issues.AddLabel(owner, repository, issue.Number, github_issue.Claimed); err != nil {
		return err
	}
	return issues.RemoveHelpWanted(owner, repository, issue)
}
