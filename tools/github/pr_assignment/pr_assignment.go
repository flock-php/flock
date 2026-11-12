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

type assignmentTransition uint8

const (
	assignmentNoOp assignmentTransition = iota
	assignmentRecoverAutomaticClaim
	assignmentCreateAutomaticClaim
	assignmentConflict
	assignmentApprovalWithdrawn
)

type assignmentSnapshot struct {
	issue *github_issue.Issue
	claim *github_issue.Claim
}

func Execute(dependencies Dependencies, owner string, issueReference issue_reference.Reference, pullRequestNumber int, author string) (Result, error) {
	claim := issueClaim{
		dependencies:      dependencies,
		owner:             owner,
		issueReference:    issueReference,
		pullRequestNumber: pullRequestNumber,
		author:            author,
	}
	transition, snapshot, err := claim.evaluate()
	if err != nil {
		return Result{}, err
	}
	return claim.apply(transition, snapshot)
}

type issueClaim struct {
	dependencies      Dependencies
	owner             string
	issueReference    issue_reference.Reference
	pullRequestNumber int
	author            string
}

func (c issueClaim) evaluate() (assignmentTransition, assignmentSnapshot, error) {
	issue, err := c.dependencies.Issues.Get(c.owner, c.issueReference.Repository, c.issueReference.Number)
	if err != nil {
		return assignmentNoOp, assignmentSnapshot{}, err
	}
	issueState := github_issue.IssueApprovalFor(issue, c.author)
	claim := (*github_issue.Claim)(nil)
	if issueState != github_issue.IssueApprovalDenied {
		claim, err = c.dependencies.Issues.FindClaim(c.owner, c.issueReference.Repository, c.issueReference.Number)
		if err != nil {
			return assignmentNoOp, assignmentSnapshot{}, err
		}
	}
	authorization, err := github_issue.AuthorizationFor(issueState, github_issue.ClaimOwnershipFor(issue, claim, c.author, c.pullRequestNumber))
	if err != nil {
		return assignmentNoOp, assignmentSnapshot{}, err
	}
	transition, err := assignmentTransitionFor(authorization)
	if err != nil {
		return assignmentNoOp, assignmentSnapshot{}, err
	}
	return transition, assignmentSnapshot{issue: issue, claim: claim}, nil
}

func assignmentTransitionFor(authorization github_issue.AuthorizationState) (assignmentTransition, error) {
	switch authorization {
	case github_issue.AuthorizationDenied:
		return assignmentApprovalWithdrawn, nil
	case github_issue.AuthorizationMaintainerAssignment:
		return assignmentNoOp, nil
	case github_issue.AuthorizationAutomaticClaimOwner:
		return assignmentRecoverAutomaticClaim, nil
	case github_issue.AuthorizationCreateAutomaticClaim:
		return assignmentCreateAutomaticClaim, nil
	case github_issue.AuthorizationAutomaticClaimConflict:
		return assignmentConflict, nil
	default:
		return assignmentNoOp, fmt.Errorf("unknown authorization state %d", authorization)
	}
}

func (c issueClaim) apply(transition assignmentTransition, snapshot assignmentSnapshot) (Result, error) {
	switch transition {
	case assignmentCreateAutomaticClaim:
		if err := c.dependencies.Issues.CreateClaim(c.owner, c.issueReference.Repository, c.issueReference.Number, c.pullRequestNumber, c.author); err != nil {
			return Result{}, err
		}
		return c.assignClaimOwner(snapshot.issue, true)
	case assignmentRecoverAutomaticClaim:
		return c.assignClaimOwner(snapshot.issue, true)
	case assignmentConflict:
		return Result{
			ClaimedBy: snapshot.claim.Author,
		}, nil
	case assignmentApprovalWithdrawn:
		return Result{
			ApprovalWithdrawn: true,
		}, nil
	case assignmentNoOp:
		return Result{}, nil
	default:
		return Result{}, fmt.Errorf("unknown assignment transition %d", transition)
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
