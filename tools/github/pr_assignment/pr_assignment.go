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
	if issue == nil || issue.IsPullRequest || issue.State != "open" {
		return Result{ApprovalWithdrawn: true}, nil
	}
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
	if !approvedFor(issue, c.author) {
		return Result{ApprovalWithdrawn: true}, nil
	}
	if len(issue.Assignees) != 0 {
		return Result{}, nil
	}
	if err := c.dependencies.Issues.CreateClaim(c.owner, c.issueReference.Repository, c.issueReference.Number, c.pullRequestNumber, c.author); err != nil {
		return Result{}, err
	}
	return c.assignClaimOwner(issue, true)
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
	result, err := c.markClaimed(issue)
	result.AutomatedClaim = automatedClaim
	return result, err
}

func (c issueClaim) markClaimed(issue *github_issue.Issue) (Result, error) {
	return Result{}, markClaimed(c.dependencies.Issues, c.owner, c.issueReference.Repository, issue)
}

func markClaimed(issues IssueClaimer, owner, repository string, issue *github_issue.Issue) error {
	if err := issues.AddLabel(owner, repository, issue.Number, github_issue.Claimed); err != nil {
		return err
	}
	return issues.RemoveHelpWanted(owner, repository, issue)
}

func approvedFor(issue *github_issue.Issue, author string) bool {
	if issue == nil || issue.IsPullRequest || issue.State != "open" {
		return false
	}
	if len(issue.Assignees) != 0 {
		return github_issue.HasAssignee(issue, author)
	}
	return github_issue.HasHelpWanted(issue)
}
