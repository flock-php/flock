// Package issue_assignment removes public availability from assigned issues.
package issue_assignment

import (
	"fmt"

	"flock/tools/github/github_issue"
)

type AssignmentDisplayService interface {
	Get(owner, repository string, number int) (*github_issue.Issue, error)
	FindClaim(owner, repository string, number int) (*github_issue.Claim, error)
	RemoveHelpWanted(owner, repository string, issue *github_issue.Issue) error
	RemoveClaimed(owner, repository string, issue *github_issue.Issue) error
}

type assignmentDisplayTransition uint8

const (
	skipAssignmentDisplay assignmentDisplayTransition = iota
	removeHelpWantedDisplay
	removeHelpWantedAndClaimedDisplay
)

func assignmentDisplayTransitionFor(issue *github_issue.Issue, claim *github_issue.Claim) assignmentDisplayTransition {
	if github_issue.ClaimOwnershipFor(issue, claim, "", 0) == github_issue.ClaimOwnershipSuperseded {
		return removeHelpWantedAndClaimedDisplay
	}
	return removeHelpWantedDisplay
}

func Execute(issues AssignmentDisplayService, owner, repository string, issue *github_issue.Issue) error {
	machine := assignmentDisplayMachine{issues: issues, owner: owner, repository: repository, issue: issue}
	decision, err := machine.evaluate()
	if err != nil {
		return err
	}
	return machine.apply(decision)
}

type assignmentDisplayDecision struct {
	transition assignmentDisplayTransition
	issue      *github_issue.Issue
}

type assignmentDisplayMachine struct {
	issues     AssignmentDisplayService
	owner      string
	repository string
	issue      *github_issue.Issue
}

func (m assignmentDisplayMachine) evaluate() (assignmentDisplayDecision, error) {
	if m.issue == nil {
		return assignmentDisplayDecision{transition: skipAssignmentDisplay}, nil
	}
	currentIssue, err := m.issues.Get(m.owner, m.repository, m.issue.Number)
	if err != nil {
		return assignmentDisplayDecision{}, err
	}
	if currentIssue == nil || len(currentIssue.Assignees) == 0 {
		return assignmentDisplayDecision{transition: skipAssignmentDisplay}, nil
	}
	claim, err := m.issues.FindClaim(m.owner, m.repository, currentIssue.Number)
	if err != nil {
		return assignmentDisplayDecision{}, err
	}
	return assignmentDisplayDecision{
		transition: assignmentDisplayTransitionFor(currentIssue, claim),
		issue:      currentIssue,
	}, nil
}

func (m assignmentDisplayMachine) apply(decision assignmentDisplayDecision) error {
	switch decision.transition {
	case skipAssignmentDisplay:
		return nil
	case removeHelpWantedDisplay:
		return m.issues.RemoveHelpWanted(m.owner, m.repository, decision.issue)
	case removeHelpWantedAndClaimedDisplay:
		if err := m.issues.RemoveHelpWanted(m.owner, m.repository, decision.issue); err != nil {
			return err
		}
		return m.issues.RemoveClaimed(m.owner, m.repository, decision.issue)
	default:
		return fmt.Errorf("unknown assignment display transition %d", decision.transition)
	}
}
