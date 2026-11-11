package main

import (
	"fmt"

	"flock/tools/github/github_pr"
	"flock/tools/github/milestone_transfer"
	"flock/tools/github/pr_assignment"
	"flock/tools/github/pr_gate"
)

const maxApprovalRetries = 3

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
		result, err := pr_gate.Execute(s.Gate, owner, repository, gateEvent)
		if err != nil {
			return err
		}
		if result.ApprovedIssue == nil {
			return nil
		}
		approvedIssue := *result.ApprovedIssue
		if result.ClaimedBy != "" {
			return pr_gate.CloseClaimedIssue(s.Gate.PullRequests, owner, repository, event.Number, approvedIssue, result.ClaimedBy)
		}
		assignment, err := pr_assignment.Execute(s.Assignment, owner, approvedIssue, event.Author)
		if err != nil {
			return err
		}
		if assignment.ClaimedBy != "" {
			return pr_gate.CloseClaimedIssue(s.Gate.PullRequests, owner, repository, event.Number, approvedIssue, assignment.ClaimedBy)
		}
		if !assignment.ApprovalWithdrawn {
			if err := milestone_transfer.Execute(s.Milestones, owner, repository, event.Number, approvedIssue); err != nil {
				return err
			}
			return pr_gate.CompleteClaim(s.Gate.Issues, owner, repository, event.Number)
		}
		if retries == maxApprovalRetries {
			return fmt.Errorf("issue approval changed repeatedly while processing pull request #%d", event.Number)
		}
		gateEvent = pr_gate.Event{Action: "synchronize", Number: event.Number}
	}
}
