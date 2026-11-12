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
	DeleteClaim(owner, repository string, commentID int64) error
	LatestAssignment(owner, repository string, number int, username string) (*Assignment, error)
	RemoveAssignee(owner, repository string, number int, username string) error
	RemoveClaimed(owner, repository string, issue *github_issue.Issue) error
	RestoreHelpWanted(owner, repository string, issue *github_issue.Issue) error
}

type Dependencies struct {
	Issues IssueClaimer
}

type ResultState uint8

const (
	ResultNoOp ResultState = iota
	ResultAutomaticClaim
	ResultConflict
	ResultApprovalWithdrawn
)

type Result struct {
	State    ResultState
	Claimant string
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

type releaseTransition uint8

const (
	releaseNoOp releaseTransition = iota
	releaseDeleteClaim
	releaseAutomaticState
)

type releaseAction uint8

const (
	deleteIssueClaim releaseAction = iota
	removeAutomaticAssignee
	removeAutomaticClaimedLabel
	restoreAutomaticHelpWanted
)

type releaseDecision struct {
	transition releaseTransition
	actions    []releaseAction
	issue      *github_issue.Issue
	claim      *github_issue.Claim
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
		return c.assignClaimOwner(snapshot.issue)
	case assignmentRecoverAutomaticClaim:
		return c.assignClaimOwner(snapshot.issue)
	case assignmentConflict:
		return Result{
			State:    ResultConflict,
			Claimant: snapshot.claim.Author,
		}, nil
	case assignmentApprovalWithdrawn:
		return Result{
			State: ResultApprovalWithdrawn,
		}, nil
	case assignmentNoOp:
		return Result{}, nil
	default:
		return Result{}, fmt.Errorf("unknown assignment transition %d", transition)
	}
}

func (c issueClaim) assignClaimOwner(issue *github_issue.Issue) (Result, error) {
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
	return Result{State: ResultAutomaticClaim}, nil
}

func markClaimed(issues IssueClaimer, owner, repository string, issue *github_issue.Issue) error {
	if err := issues.AddLabel(owner, repository, issue.Number, github_issue.Claimed); err != nil {
		return err
	}
	return issues.RemoveHelpWanted(owner, repository, issue)
}

type Assignment = github_issue.Assignment

func Release(dependencies Dependencies, owner string, issue issue_reference.Reference, pullRequestNumber int, author string) error {
	machine := releaseMachine{
		dependencies:      dependencies,
		owner:             owner,
		issue:             issue,
		pullRequestNumber: pullRequestNumber,
		author:            author,
	}
	decision, err := machine.evaluate()
	if err != nil {
		return err
	}
	return machine.apply(decision)
}

func matchesClaim(claim *github_issue.Claim, pullRequestNumber int, author string) bool {
	return claim != nil && claim.PullRequestNumber == pullRequestNumber && claim.Author == author
}

type releaseEvaluationState uint8

const (
	readReleaseIssue releaseEvaluationState = iota
	readReleaseClaim
)

type releaseMachine struct {
	dependencies      Dependencies
	owner             string
	issue             issue_reference.Reference
	pullRequestNumber int
	author            string
}

func (m releaseMachine) evaluate() (releaseDecision, error) {
	state := readReleaseIssue
	var current *github_issue.Issue
	var claim *github_issue.Claim
	for {
		switch state {
		case readReleaseIssue:
			var err error
			current, err = m.dependencies.Issues.Get(m.owner, m.issue.Repository, m.issue.Number)
			if err != nil {
				return releaseDecision{}, err
			}
			if current == nil {
				return releaseDecision{transition: releaseNoOp}, nil
			}
			state = readReleaseClaim
		case readReleaseClaim:
			var err error
			claim, err = m.dependencies.Issues.FindClaim(m.owner, m.issue.Repository, m.issue.Number)
			if err != nil {
				return releaseDecision{}, err
			}
			decision := releaseDecisionForClaim(current, claim, m.pullRequestNumber, m.author)
			switch decision.transition {
			case releaseNoOp, releaseDeleteClaim, releaseAutomaticState:
				return decision, nil
			default:
				return releaseDecision{}, fmt.Errorf("unknown release transition %d", decision.transition)
			}
		default:
			return releaseDecision{}, fmt.Errorf("unknown release evaluation state %d", state)
		}
	}
}

func releaseDecisionForClaim(current *github_issue.Issue, claim *github_issue.Claim, pullRequestNumber int, author string) releaseDecision {
	if claim != nil && !matchesClaim(claim, pullRequestNumber, author) {
		return releaseDecision{transition: releaseNoOp}
	}
	if len(current.Assignees) > 1 || (len(current.Assignees) == 1 && !github_issue.HasAssignee(current, author)) {
		return deleteClaimDecision(current, claim)
	}
	if len(current.Assignees) == 0 && (current.State != "open" || (!github_issue.HasLabel(current, github_issue.Claimed) && github_issue.HasHelpWanted(current))) {
		return deleteClaimDecision(current, claim)
	}
	actions := make([]releaseAction, 0, 1)
	if claim != nil {
		actions = append(actions, deleteIssueClaim)
	}
	return releaseDecision{transition: releaseAutomaticState, actions: actions, issue: current, claim: claim}
}

func releaseDecisionForProvenance(current *github_issue.Issue, claim *github_issue.Claim, author string, assignment *Assignment) releaseDecision {
	if assignment == nil || assignment.Assigner != "github-actions[bot]" {
		return releaseDecision{transition: releaseNoOp, issue: current, claim: claim}
	}
	actions := make([]releaseAction, 0, 4)
	if github_issue.HasAssignee(current, author) {
		actions = append(actions, removeAutomaticAssignee)
	}
	actions = append(actions, removeAutomaticClaimedLabel, restoreAutomaticHelpWanted)
	return releaseDecision{transition: releaseAutomaticState, actions: actions, issue: current, claim: claim}
}

func deleteClaimDecision(current *github_issue.Issue, claim *github_issue.Claim) releaseDecision {
	if claim == nil {
		return releaseDecision{transition: releaseNoOp, issue: current}
	}
	return releaseDecision{transition: releaseDeleteClaim, actions: []releaseAction{deleteIssueClaim}, issue: current, claim: claim}
}

func (m releaseMachine) apply(decision releaseDecision) error {
	if err := m.applyActions(decision); err != nil {
		return err
	}
	if decision.transition != releaseAutomaticState {
		return nil
	}
	assignment, err := m.dependencies.Issues.LatestAssignment(m.owner, m.issue.Repository, m.issue.Number, m.author)
	if err != nil {
		return err
	}
	return m.applyActions(releaseDecisionForProvenance(decision.issue, decision.claim, m.author, assignment))
}

func (m releaseMachine) applyActions(decision releaseDecision) error {
	for _, action := range decision.actions {
		switch action {
		case deleteIssueClaim:
			if err := m.dependencies.Issues.DeleteClaim(m.owner, m.issue.Repository, decision.claim.CommentID); err != nil {
				return err
			}
		case removeAutomaticAssignee:
			if err := m.dependencies.Issues.RemoveAssignee(m.owner, m.issue.Repository, m.issue.Number, m.author); err != nil {
				return err
			}
		case removeAutomaticClaimedLabel:
			if err := m.dependencies.Issues.RemoveClaimed(m.owner, m.issue.Repository, decision.issue); err != nil {
				return err
			}
		case restoreAutomaticHelpWanted:
			if err := m.dependencies.Issues.RestoreHelpWanted(m.owner, m.issue.Repository, decision.issue); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown release action %d", action)
		}
	}
	return nil
}
