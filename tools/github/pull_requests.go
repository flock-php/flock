package main

import (
	"fmt"

	"flock/tools/github/github_pr"
	"flock/tools/github/issue_reference"
	"flock/tools/github/milestone_transfer"
	"flock/tools/github/pr_assignment"
	"flock/tools/github/pr_gate"
)

const maxApprovalRetries = 3

type workflowTransition uint8

const (
	workflowStop workflowTransition = iota
	workflowCloseConflict
	workflowCompleteApproval
	workflowRetryApproval
)

type workflowContext struct {
	owner             string
	repository        string
	pullRequestNumber int
	issue             issue_reference.Reference
	claimant          string
	automatedClaim    bool
}

// PullRequestEventService coordinates contribution policies for pull request events.
type PullRequestEventService struct {
	Assignment pr_assignment.Dependencies
	Gate       pr_gate.Dependencies
	Milestones milestone_transfer.PullRequestMilestoneService
}

func (s PullRequestEventService) Run(owner, repository string, data []byte) error {
	event, err := github_pr.ParseEvent(data)
	if err != nil {
		return err
	}
	gateEvent := pr_gate.Event{
		Action:     event.Action,
		Number:     event.Number,
		BodyEdited: event.BodyEdited,
		BaseEdited: event.BaseEdited,
	}
	for retries := 0; ; retries++ {
		gateResult, err := pr_gate.Execute(s.Gate, owner, repository, gateEvent)
		if err != nil {
			return err
		}
		context := workflowContext{
			owner:             owner,
			repository:        repository,
			pullRequestNumber: event.Number,
		}
		if gateResult.ApprovedIssue == nil {
			_, err := s.apply(workflowStop, context)
			return err
		}
		context.issue = *gateResult.ApprovedIssue
		context.claimant = event.Author
		assignment, err := pr_assignment.Execute(s.Assignment, owner, context.issue, event.Number, event.Author)
		if err != nil {
			return err
		}
		if assignment.ClaimedBy != "" {
			context.claimant = assignment.ClaimedBy
		}
		context.automatedClaim = assignment.AutomatedClaim
		retry, err := s.apply(workflowTransitionFor(gateResult, assignment), context)
		if err != nil {
			return err
		}
		if !retry {
			return nil
		}
		if retries == maxApprovalRetries {
			return fmt.Errorf("issue approval changed repeatedly while processing pull request #%d", event.Number)
		}
		gateEvent = pr_gate.Event{Action: "synchronize", Number: event.Number}
	}
}

func workflowTransitionFor(gate pr_gate.Result, assignment pr_assignment.Result) workflowTransition {
	if gate.ApprovedIssue == nil {
		return workflowStop
	}
	if assignment.ClaimedBy != "" {
		return workflowCloseConflict
	}
	if assignment.ApprovalWithdrawn {
		return workflowRetryApproval
	}
	return workflowCompleteApproval
}

func (s PullRequestEventService) apply(transition workflowTransition, context workflowContext) (retry bool, err error) {
	switch transition {
	case workflowStop:
		return false, nil
	case workflowCloseConflict:
		return false, pr_gate.CloseClaimedIssue(s.Gate.PullRequests, context.owner, context.repository, context.pullRequestNumber, context.issue, context.claimant)
	case workflowCompleteApproval:
		if context.automatedClaim {
			if err := pr_gate.EnsureClaimComment(s.Gate.PullRequests, context.owner, context.repository, context.pullRequestNumber, context.issue, context.claimant); err != nil {
				return false, err
			}
		}
		if err := milestone_transfer.Execute(s.Milestones, context.owner, context.repository, context.pullRequestNumber, context.issue); err != nil {
			return false, err
		}
		return false, pr_gate.CompleteClaim(s.Gate.Issues, context.owner, context.repository, context.pullRequestNumber)
	case workflowRetryApproval:
		return true, nil
	default:
		return false, fmt.Errorf("unknown pull request workflow transition %d", transition)
	}
}
