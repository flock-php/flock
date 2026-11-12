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
}

type workflowDecision struct {
	transition workflowTransition
	record     claimRecordTransition
	claimant   string
}

type claimRecordTransition uint8

const (
	keepClaimRecord claimRecordTransition = iota
	releaseClaimRecord
	createClaimRecord
	replaceClaimRecord
)

type retryTransition uint8

const (
	finishWorkflow retryTransition = iota
	retryWorkflow
	retryLimitReached
)

type reconciliationTransition uint8

const (
	continueAfterReconciliation reconciliationTransition = iota
	stopAfterReconciliation
	releaseAndContinueReconciliation
	releaseAndStopReconciliation
)

type reconciliationDecision struct {
	transition reconciliationTransition
	record     *github_pr.ClaimRecord
}

type linkedIssueTransition uint8

const (
	linkedIssueMissing linkedIssueTransition = iota
	linkedIssuePresent
)

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
	reconciliation, err := s.reconcileClaim(owner, repository, event)
	if err != nil {
		return fmt.Errorf("reconcile claim for pull request #%d: %w", event.Number, err)
	}
	switch reconciliation {
	case stopAfterReconciliation, releaseAndStopReconciliation:
		return nil
	case continueAfterReconciliation, releaseAndContinueReconciliation:
		// Continue with gate evaluation selected by reconciliation.
	default:
		return fmt.Errorf("unknown reconciliation transition %d", reconciliation)
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
			return fmt.Errorf("evaluate pull request #%d: %w", event.Number, err)
		}
		gateDecision, err := workflowTransitionFor(gateResult, pr_assignment.Result{}, event.Author)
		if err != nil {
			return fmt.Errorf("select pull request workflow transition #%d: %w", event.Number, err)
		}
		if gateDecision.transition == workflowStop {
			_, err := s.apply(gateDecision.transition, workflowContext{owner: owner, repository: repository, pullRequestNumber: event.Number})
			return err
		}
		context := workflowContext{
			owner:             owner,
			repository:        repository,
			pullRequestNumber: event.Number,
			issue:             *gateResult.ApprovedIssue,
			claimant:          gateDecision.claimant,
		}
		record, err := s.ensureClaimRecord(owner, repository, event.Number, context.issue, event.Author)
		if err != nil {
			return fmt.Errorf("create claim record for pull request #%d issue %s: %w", event.Number, context.issue, err)
		}
		assignment, err := pr_assignment.Execute(s.Assignment, owner, context.issue, event.Number, event.Author)
		if err != nil {
			return fmt.Errorf("assign issue %s for pull request #%d: %w", context.issue, event.Number, err)
		}
		decision, err := workflowTransitionFor(gateResult, assignment, event.Author)
		if err != nil {
			return fmt.Errorf("select pull request workflow transition #%d: %w", event.Number, err)
		}
		context.claimant = decision.claimant
		if err := s.applyClaimRecordTransition(decision.record, owner, repository, event.Number, record); err != nil {
			return err
		}
		retry, err := s.apply(decision.transition, context)
		if err != nil {
			return fmt.Errorf("apply pull request #%d issue %s: %w", event.Number, context.issue, err)
		}
		retryDecision := retryTransitionFor(retry, retries)
		switch retryDecision {
		case finishWorkflow:
			return nil
		case retryLimitReached:
			return fmt.Errorf("issue approval changed repeatedly while processing pull request #%d", event.Number)
		case retryWorkflow:
			gateEvent = pr_gate.Event{Action: "synchronize", Number: event.Number}
		default:
			return fmt.Errorf("unknown pull request retry transition %d", retryDecision)
		}
	}
}

func (s PullRequestEventService) reconcileClaim(owner, repository string, event github_pr.Event) (reconciliationTransition, error) {
	machine := reconciliationMachine{
		service:    s,
		owner:      owner,
		repository: repository,
		event:      event,
	}
	decision, err := machine.evaluate()
	if err != nil {
		return 0, err
	}
	return machine.apply(decision)
}

func (s PullRequestEventService) ensureClaimRecord(owner, repository string, pullRequestNumber int, issue issue_reference.Reference, author string) (*github_pr.ClaimRecord, error) {
	machine := claimRecordMachine{
		service:           s,
		owner:             owner,
		repository:        repository,
		pullRequestNumber: pullRequestNumber,
		issue:             issue,
		author:            author,
	}
	return machine.run()
}

func (s PullRequestEventService) releaseClaimRecord(owner, repository string, pullRequestNumber int, record *github_pr.ClaimRecord) error {
	if err := pr_assignment.Release(s.Assignment, owner, record.Issue, pullRequestNumber, record.Author); err != nil {
		return fmt.Errorf("release issue %s for pull request #%d: %w", record.Issue, pullRequestNumber, err)
	}
	if err := s.Gate.PullRequests.DeleteClaimRecord(owner, repository, record); err != nil {
		return fmt.Errorf("delete claim record for pull request #%d issue %s: %w", pullRequestNumber, record.Issue, err)
	}
	return nil
}

func (s PullRequestEventService) applyClaimRecordTransition(transition claimRecordTransition, owner, repository string, pullRequestNumber int, record *github_pr.ClaimRecord) error {
	switch transition {
	case keepClaimRecord:
		return nil
	case releaseClaimRecord:
		return s.releaseClaimRecord(owner, repository, pullRequestNumber, record)
	default:
		return fmt.Errorf("unknown claim record transition %d", transition)
	}
}

type claimRecordMachine struct {
	service           PullRequestEventService
	owner             string
	repository        string
	pullRequestNumber int
	issue             issue_reference.Reference
	author            string
}

func (m claimRecordMachine) run() (*github_pr.ClaimRecord, error) {
	record, err := m.service.Gate.PullRequests.FindClaimRecord(m.owner, m.repository, m.pullRequestNumber)
	if err != nil {
		return nil, err
	}
	transition := claimRecordTransitionFor(record, m.issue, m.author)
	switch transition {
	case keepClaimRecord:
		return record, nil
	case createClaimRecord:
		return m.service.Gate.PullRequests.CreateClaimRecord(m.owner, m.repository, m.pullRequestNumber, m.issue, m.author)
	case replaceClaimRecord:
		if err := m.service.releaseClaimRecord(m.owner, m.repository, m.pullRequestNumber, record); err != nil {
			return nil, err
		}
		return m.service.Gate.PullRequests.CreateClaimRecord(m.owner, m.repository, m.pullRequestNumber, m.issue, m.author)
	default:
		return nil, fmt.Errorf("unknown claim record transition %d", transition)
	}
}

func claimRecordTransitionFor(record *github_pr.ClaimRecord, issue issue_reference.Reference, author string) claimRecordTransition {
	if record == nil {
		return createClaimRecord
	}
	if record.Issue == issue && record.Author == author {
		return keepClaimRecord
	}
	return replaceClaimRecord
}

type reconciliationMachine struct {
	service    PullRequestEventService
	owner      string
	repository string
	event      github_pr.Event
}

func (m reconciliationMachine) evaluate() (reconciliationDecision, error) {
	pullRequest, err := m.service.Gate.PullRequests.Get(m.owner, m.repository, m.event.Number)
	if err != nil {
		return reconciliationDecision{}, fmt.Errorf("read current pull request: %w", err)
	}
	if m.event.Action == "closed" && pullRequest == nil {
		return reconciliationDecision{transition: stopAfterReconciliation}, nil
	}
	record, err := m.service.Gate.PullRequests.FindClaimRecord(m.owner, m.repository, m.event.Number)
	if err != nil {
		return reconciliationDecision{}, fmt.Errorf("read claim record: %w", err)
	}
	if record == nil {
		if m.event.Action == "closed" {
			return reconciliationDecision{transition: stopAfterReconciliation}, nil
		}
		return reconciliationDecision{transition: continueAfterReconciliation}, nil
	}
	if pullRequest == nil || pullRequest.Merged {
		return reconciliationDecision{transition: stopAfterReconciliation, record: record}, nil
	}
	if m.event.Action == "closed" || pullRequest.State == "closed" {
		return reconciliationDecision{transition: releaseAndStopReconciliation, record: record}, nil
	}
	if pullRequest.BaseRef != "main" {
		return reconciliationDecision{transition: releaseAndContinueReconciliation, record: record}, nil
	}
	linkedIssues, err := m.service.Gate.PullRequests.ListLinkedIssues(m.owner, m.repository, m.event.Number)
	if err != nil {
		return reconciliationDecision{}, fmt.Errorf("read linked issues: %w", err)
	}
	linkedIssueTransition := linkedIssueTransitionFor(linkedIssues, m.owner, m.repository, record.Issue)
	switch linkedIssueTransition {
	case linkedIssueMissing:
		return reconciliationDecision{transition: releaseAndContinueReconciliation, record: record}, nil
	case linkedIssuePresent:
		return reconciliationDecision{transition: continueAfterReconciliation}, nil
	default:
		return reconciliationDecision{}, fmt.Errorf("unknown linked issue transition %d", linkedIssueTransition)
	}
}

func (m reconciliationMachine) apply(decision reconciliationDecision) (reconciliationTransition, error) {
	switch decision.transition {
	case continueAfterReconciliation:
		return continueAfterReconciliation, nil
	case stopAfterReconciliation:
		return stopAfterReconciliation, nil
	case releaseAndContinueReconciliation:
		if err := m.service.releaseClaimRecord(m.owner, m.repository, m.event.Number, decision.record); err != nil {
			return 0, err
		}
		return releaseAndContinueReconciliation, nil
	case releaseAndStopReconciliation:
		if err := m.service.releaseClaimRecord(m.owner, m.repository, m.event.Number, decision.record); err != nil {
			return 0, err
		}
		return releaseAndStopReconciliation, nil
	default:
		return 0, fmt.Errorf("unknown reconciliation transition %d", decision.transition)
	}
}

func linkedIssueTransitionFor(linkedIssues []github_pr.LinkedIssue, owner, repository string, issue issue_reference.Reference) linkedIssueTransition {
	if issue.Repository != repository {
		return linkedIssueMissing
	}
	for _, linkedIssue := range linkedIssues {
		if linkedIssue.Owner == owner && linkedIssue.Repository == repository && linkedIssue.Number == issue.Number {
			return linkedIssuePresent
		}
	}
	return linkedIssueMissing
}

func workflowTransitionFor(gate pr_gate.Result, assignment pr_assignment.Result, defaultClaimant string) (workflowDecision, error) {
	if gate.ApprovedIssue == nil {
		return workflowDecision{transition: workflowStop, claimant: defaultClaimant}, nil
	}
	switch assignment.State {
	case pr_assignment.ResultNoOp:
		return workflowDecision{transition: workflowCompleteApproval, record: releaseClaimRecord, claimant: defaultClaimant}, nil
	case pr_assignment.ResultAutomaticClaim:
		return workflowDecision{transition: workflowCompleteApproval, record: keepClaimRecord, claimant: defaultClaimant}, nil
	case pr_assignment.ResultConflict:
		return workflowDecision{transition: workflowCloseConflict, record: releaseClaimRecord, claimant: assignment.Claimant}, nil
	case pr_assignment.ResultApprovalWithdrawn:
		return workflowDecision{transition: workflowRetryApproval, record: releaseClaimRecord, claimant: defaultClaimant}, nil
	default:
		return workflowDecision{}, fmt.Errorf("unknown assignment result state %d", assignment.State)
	}
}

func retryTransitionFor(retry bool, retries int) retryTransition {
	if !retry {
		return finishWorkflow
	}
	if retries == maxApprovalRetries {
		return retryLimitReached
	}
	return retryWorkflow
}

func (s PullRequestEventService) apply(transition workflowTransition, context workflowContext) (retry bool, err error) {
	switch transition {
	case workflowStop:
		return false, nil
	case workflowCloseConflict:
		return false, pr_gate.CloseClaimedIssue(s.Gate.PullRequests, context.owner, context.repository, context.pullRequestNumber, context.issue, context.claimant)
	case workflowCompleteApproval:
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
