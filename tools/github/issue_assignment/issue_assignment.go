// Package issue_assignment removes public availability from assigned issues.
package issue_assignment

import "flock/tools/github/github_issue"

type AssignmentDisplayService interface {
	Get(owner, repository string, number int) (*github_issue.Issue, error)
	FindClaim(owner, repository string, number int) (*github_issue.Claim, error)
	RemoveHelpWanted(owner, repository string, issue *github_issue.Issue) error
	RemoveClaimed(owner, repository string, issue *github_issue.Issue) error
}

type assignmentDisplayTransition uint8

const (
	keepClaimedDisplay assignmentDisplayTransition = iota
	removeClaimedDisplay
)

func assignmentDisplayTransitionFor(issue *github_issue.Issue, claim *github_issue.Claim) assignmentDisplayTransition {
	if github_issue.ClaimOwnershipFor(issue, claim, "", 0) == github_issue.ClaimOwnershipSuperseded {
		return removeClaimedDisplay
	}
	return keepClaimedDisplay
}

func Execute(issues AssignmentDisplayService, owner, repository string, issue *github_issue.Issue) error {
	if issue == nil {
		return nil
	}
	currentIssue, err := issues.Get(owner, repository, issue.Number)
	if err != nil {
		return err
	}
	if currentIssue == nil {
		return nil
	}
	if len(currentIssue.Assignees) == 0 {
		return nil
	}
	claim, err := issues.FindClaim(owner, repository, currentIssue.Number)
	if err != nil {
		return err
	}
	if err := issues.RemoveHelpWanted(owner, repository, currentIssue); err != nil {
		return err
	}
	if assignmentDisplayTransitionFor(currentIssue, claim) == removeClaimedDisplay {
		return issues.RemoveClaimed(owner, repository, currentIssue)
	}
	return nil
}
