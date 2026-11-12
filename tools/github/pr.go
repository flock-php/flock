package main

import (
	"fmt"
	"time"
)

const maxApprovalRetries = 3

type prState uint8

const (
	prStateStart prState = iota
	prStateReadPullRequest
	prStateReconcileRecord
	prStateReconcileLinks
	prStateGateReadPullRequest
	prStateGateClosed
	prStateGateFiles
	prStateGateLinks
	prStateGateIssue
	prStateGateClaim
	prStateGateWait
	prStateReopenState
	prStateReopenComment
	prStateEnsureRecord
	prStateAssignIssue
	prStateAssignClaim
	prStateAssignCreateClaim
	prStateAssignAddAssignee
	prStateAssignAddClaimed
	prStateAssignRemoveHelpWanted
	prStateCloseComment
	prStateCloseState
	prStateReleaseIssue
	prStateReleaseClaim
	prStateReleaseAfterClaim
	prStateReleaseAssignment
	prStateReleaseAssignee
	prStateReleaseHelpWanted
	prStateReleaseDeleteRecord
	prStateDeleteRecord
	prStateMilestoneRead
	prStateMilestoneSet
	prStateMilestoneRemove
	prStateMilestoneWait
	prStateCompleteLabel
	prStateDone
)

type prSnapshot struct {
	event        pullRequestEvent
	owner        string
	repository   string
	pullRequest  *pullRequest
	record       *claimRecord
	linked       []linkedIssue
	issue        *issue
	claim        *claim
	approved     issueReference
	claimant     string
	files        []file
	index        int
	attempts     int
	removals     int
	milestone    *int
	release      *claimRecord
	afterRelease prState
	assignment   *assignment
	reopen       bool
}

type prMachine struct {
	state    prState
	snapshot prSnapshot
}

func newPRMachine(owner, repository string, event pullRequestEvent) *prMachine {
	return &prMachine{state: prStateStart, snapshot: prSnapshot{owner: owner, repository: repository, event: event}}
}

func (m *prMachine) step(result commandResult) (command, error) {
	if result.err != nil {
		if m.state == prStateMilestoneRemove && m.snapshot.removals < 3 {
			m.snapshot.removals++
			m.state = prStateMilestoneWait
			return command{kind: commandWait, delay: 3 * time.Second}, nil
		}
		return command{}, result.err
	}
	s := &m.snapshot
	switch m.state {
	case prStateStart:
		m.state = prStateReadPullRequest
		return command{kind: commandReadPullRequest, pullRequestNumber: s.event.Number}, nil
	case prStateReadPullRequest:
		s.pullRequest = result.pullRequest
		if s.event.Action == "closed" && s.pullRequest == nil {
			return m.done()
		}
		m.state = prStateReconcileRecord
		return command{kind: commandReadClaimRecord, pullRequestNumber: s.event.Number}, nil
	case prStateReconcileRecord:
		s.record = result.claimRecord
		if s.record == nil {
			if s.event.Action == "closed" {
				return m.done()
			}
			return m.startGate()
		}
		if s.pullRequest == nil || s.pullRequest.Merged {
			return m.done()
		}
		if s.event.Action == "closed" || s.pullRequest.State == "closed" || s.pullRequest.BaseRef != "main" {
			return m.startRelease(s.record, prStateDone)
		}
		m.state = prStateReconcileLinks
		return command{kind: commandListLinkedIssues, pullRequestNumber: s.event.Number}, nil
	case prStateReconcileLinks:
		s.linked = result.linkedIssues
		if !containsIssue(s.linked, s.owner, s.repository, s.record.Issue) {
			return m.startRelease(s.record, prStateGateReadPullRequest)
		}
		return m.startGate()
	case prStateGateReadPullRequest:
		s.pullRequest = result.pullRequest
		if s.pullRequest == nil || bypassesGate(s.pullRequest.Author, s.pullRequest.AuthorAssociation) {
			return m.done()
		}
		if s.pullRequest.State == "closed" {
			if !s.event.BodyEdited || s.pullRequest.Merged || s.pullRequest.BaseRef != "main" {
				return m.done()
			}
			m.state = prStateGateClosed
			return command{kind: commandReadGateClosed, pullRequestNumber: s.event.Number}, nil
		}
		if s.event.Action == "edited" && !s.event.BaseEdited {
			return m.done()
		}
		if s.pullRequest.BaseRef != "main" {
			return m.close("wrong base")
		}
		m.state = prStateGateFiles
		return command{kind: commandListPullRequestFiles, pullRequestNumber: s.event.Number}, nil
	case prStateGateClosed:
		if !result.gateClosed {
			return m.done()
		}
		s.reopen = true
		return m.startLinkedScan()
	case prStateGateFiles:
		if docsOnly(result.files) {
			return m.done()
		}
		return m.startLinkedScan()
	case prStateGateLinks:
		s.linked = result.linkedIssues
		s.index = 0
		s.claimant = ""
		return m.nextLinkedIssue()
	case prStateGateIssue:
		s.issue = result.issue
		if s.issue == nil || issueApprovalFor(s.issue, s.pullRequest.Author.Login) == issueApprovalDenied {
			return m.nextLinkedIssue()
		}
		m.state = prStateGateClaim
		return command{kind: commandReadIssueClaim, issue: s.approved}, nil
	case prStateGateClaim:
		s.claim = result.claim
		authorization := authorizationFor(issueApprovalFor(s.issue, s.pullRequest.Author.Login), claimOwnershipFor(s.issue, s.claim, s.pullRequest.Author.Login, s.event.Number))
		if authorization == authorizationMaintainerAssignment || authorization == authorizationAutomaticClaimOwner || authorization == authorizationCreateAutomaticClaim {
			if s.reopen {
				m.state = prStateReopenState
				return command{kind: commandSetPullRequestState, pullRequestNumber: s.event.Number, state: "open"}, nil
			}
			return m.ensureRecord()
		}
		if authorization == authorizationAutomaticClaimConflict && s.claimant == "" {
			s.claimant = s.claim.Author
		}
		return m.nextLinkedIssue()
	case prStateGateWait:
		return m.startGate()
	case prStateReopenState:
		m.state = prStateReopenComment
		return command{kind: commandCreateComment, pullRequestNumber: s.event.Number, comment: fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", s.pullRequest.Author.Login, s.approved)}, nil
	case prStateReopenComment:
		s.reopen = false
		return m.ensureRecord()
	case prStateEnsureRecord:
		if s.record == nil {
			s.record = result.claimRecord
		}
		if s.record != nil && s.record.Issue == s.approved && s.record.Author == s.pullRequest.Author.Login {
			return m.startAssignment()
		}
		if s.record != nil {
			return m.startRelease(s.record, prStateEnsureRecord)
		}
		m.state = prStateAssignIssue
		return command{kind: commandCreateClaimRecord, pullRequestNumber: s.event.Number, issue: s.approved, username: s.pullRequest.Author.Login}, nil
	case prStateAssignIssue:
		if result.claimRecord != nil {
			s.record = result.claimRecord
			return m.startAssignment()
		}
		s.issue = result.issue
		m.state = prStateAssignClaim
		return command{kind: commandReadIssueClaim, issue: s.approved}, nil
	case prStateAssignClaim:
		s.claim = result.claim
		return m.applyAssignment()
	case prStateAssignCreateClaim:
		m.state = prStateAssignAddAssignee
		return command{kind: commandAddIssueAssignee, issue: s.approved, username: s.pullRequest.Author.Login}, nil
	case prStateAssignAddAssignee:
		s.issue = result.issue
		if !issueHasAssignee(s.issue, s.pullRequest.Author.Login) {
			return command{}, fmt.Errorf("assign issue %s to @%s: GitHub did not add the assignee", s.approved, s.pullRequest.Author.Login)
		}
		m.state = prStateAssignAddClaimed
		return command{kind: commandAddIssueLabel, issue: s.approved, label: claimedLabel}, nil
	case prStateAssignAddClaimed:
		m.state = prStateAssignRemoveHelpWanted
		return command{kind: commandRemoveIssueLabel, issue: s.approved, label: helpWantedLabel}, nil
	case prStateAssignRemoveHelpWanted:
		return m.startMilestone()
	case prStateCloseComment:
		m.state = prStateCloseState
		return command{kind: commandSetPullRequestState, pullRequestNumber: s.event.Number, state: "closed"}, nil
	case prStateCloseState:
		return m.done()
	case prStateReleaseIssue:
		s.issue = result.issue
		if s.issue == nil {
			return m.deleteRecord()
		}
		m.state = prStateReleaseClaim
		return command{kind: commandReadIssueClaim, issue: s.release.Issue}, nil
	case prStateReleaseClaim:
		s.claim = result.claim
		return m.applyReleaseDecision()
	case prStateReleaseAfterClaim:
		m.state = prStateReleaseAssignment
		return command{kind: commandReadLatestAssignment, issue: s.release.Issue, username: s.release.Author}, nil
	case prStateReleaseAssignment:
		s.assignment = result.assignment
		if s.assignment == nil || s.assignment.Assigner != "github-actions[bot]" {
			return m.deleteRecord()
		}
		if issueHasAssignee(s.issue, s.release.Author) {
			m.state = prStateReleaseAssignee
			return command{kind: commandRemoveIssueAssignee, issue: s.release.Issue, username: s.release.Author}, nil
		}
		return m.releaseClaimedLabel()
	case prStateReleaseAssignee:
		return m.releaseClaimedLabel()
	case prStateReleaseHelpWanted:
		m.state = prStateReleaseDeleteRecord
		return command{kind: commandRestoreHelpWanted, issue: s.release.Issue}, nil
	case prStateReleaseDeleteRecord:
		return m.deleteRecord()
	case prStateDeleteRecord:
		return m.afterRelease()
	case prStateMilestoneRead:
		s.milestone = result.milestone
		if s.issue == nil || s.milestone == nil {
			return m.completeClaim()
		}
		m.state = prStateMilestoneSet
		return command{kind: commandSetPullRequestMilestone, pullRequestNumber: s.event.Number, milestoneNumber: s.milestone}, nil
	case prStateMilestoneSet:
		s.removals = 1
		m.state = prStateMilestoneRemove
		return command{kind: commandRemoveIssueMilestone, issue: s.approved}, nil
	case prStateMilestoneRemove:
		return m.completeClaim()
	case prStateMilestoneWait:
		m.state = prStateMilestoneRemove
		return command{kind: commandRemoveIssueMilestone, issue: s.approved}, nil
	case prStateCompleteLabel:
		m.state = prStateDone
		return command{kind: commandAddIssueLabel, pullRequestNumber: s.event.Number, label: claimedLabel}, nil
	case prStateDone:
		return command{kind: commandDone}, nil
	default:
		return command{}, fmt.Errorf("unknown pull request state %d", m.state)
	}
}

func (m *prMachine) done() (command, error) {
	m.state = prStateDone
	return command{kind: commandDone}, nil
}

func (m *prMachine) startGate() (command, error) {
	m.state = prStateGateReadPullRequest
	return command{kind: commandReadPullRequest, pullRequestNumber: m.snapshot.event.Number}, nil
}

func (m *prMachine) startLinkedScan() (command, error) {
	m.state = prStateGateLinks
	return command{kind: commandListLinkedIssues, pullRequestNumber: m.snapshot.event.Number}, nil
}

func (m *prMachine) nextLinkedIssue() (command, error) {
	s := &m.snapshot
	for s.index < len(s.linked) {
		current := s.linked[s.index]
		s.index++
		if current.Owner != s.owner || current.Repository != s.repository {
			continue
		}
		s.approved = issueReference{Repository: current.Repository, Number: current.Number}
		m.state = prStateGateIssue
		return command{kind: commandReadIssue, issue: s.approved}, nil
	}
	if s.claimant != "" {
		m.state = prStateCloseComment
		return command{kind: commandCreateComment, pullRequestNumber: s.event.Number, comment: fmt.Sprintf("Thanks for the PR, @%s! Issue %s has already been claimed by @%s, so this pull request is closed. Please check other issues labeled `help wanted`.\n\n%s", s.pullRequest.Author.Login, s.approved, s.claimant, gateMarker)}, nil
	}
	if s.reopen {
		return m.done()
	}
	if s.event.Action == "opened" && s.attempts < 2 {
		s.attempts++
		m.state = prStateGateWait
		return command{kind: commandWait, delay: 3 * time.Second}, nil
	}
	return m.close("unapproved")
}

func (m *prMachine) ensureRecord() (command, error) {
	m.state = prStateEnsureRecord
	return command{kind: commandReadClaimRecord, pullRequestNumber: m.snapshot.event.Number}, nil
}

func (m *prMachine) startAssignment() (command, error) {
	m.state = prStateAssignIssue
	return command{kind: commandReadIssue, issue: m.snapshot.approved}, nil
}

func (m *prMachine) applyAssignment() (command, error) {
	s := &m.snapshot
	authorization := authorizationFor(issueApprovalFor(s.issue, s.pullRequest.Author.Login), claimOwnershipFor(s.issue, s.claim, s.pullRequest.Author.Login, s.event.Number))
	switch authorization {
	case authorizationDenied:
		s.attempts++
		if s.attempts > maxApprovalRetries {
			return command{}, fmt.Errorf("issue approval changed repeatedly while processing pull request #%d", s.event.Number)
		}
		return m.startRelease(s.record, prStateGateReadPullRequest)
	case authorizationMaintainerAssignment:
		return m.startRelease(s.record, prStateMilestoneRead)
	case authorizationAutomaticClaimOwner:
		if !issueHasAssignee(s.issue, s.pullRequest.Author.Login) {
			m.state = prStateAssignAddAssignee
			return command{kind: commandAddIssueAssignee, issue: s.approved, username: s.pullRequest.Author.Login}, nil
		}
		m.state = prStateAssignAddClaimed
		return command{kind: commandAddIssueLabel, issue: s.approved, label: claimedLabel}, nil
	case authorizationCreateAutomaticClaim:
		m.state = prStateAssignCreateClaim
		return command{kind: commandCreateIssueClaim, issue: s.approved, pullRequestNumber: s.event.Number, username: s.pullRequest.Author.Login}, nil
	case authorizationAutomaticClaimConflict:
		s.claimant = s.claim.Author
		return m.startRelease(s.record, prStateCloseComment)
	default:
		return command{}, fmt.Errorf("unknown authorization state %d", authorization)
	}
}

func (m *prMachine) close(reason string) (command, error) {
	s := &m.snapshot
	var comment string
	if reason == "wrong base" {
		comment = fmt.Sprintf("Thanks for the PR, @%s! Pull requests go into `main`, not `%s`, so this one is closed. Please open a new pull request against `main`. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).\n\n%s", s.pullRequest.Author.Login, s.pullRequest.BaseRef, s.owner, s.repository, gateMarker)
	} else {
		comment = fmt.Sprintf("Thanks for the PR, @%s! Flock takes pull requests only for issues a maintainer has approved, so this one is closed. Edit this pull request's description to reference an approved issue, then save it to reopen automatically. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).\n\n%s", s.pullRequest.Author.Login, s.owner, s.repository, gateMarker)
	}
	m.state = prStateCloseComment
	return command{kind: commandCreateComment, pullRequestNumber: s.event.Number, comment: comment}, nil
}

func (m *prMachine) startRelease(record *claimRecord, after prState) (command, error) {
	m.snapshot.release = record
	m.snapshot.afterRelease = after
	m.state = prStateReleaseIssue
	return command{kind: commandReadIssue, issue: record.Issue}, nil
}

func (m *prMachine) applyReleaseDecision() (command, error) {
	s := &m.snapshot
	matching := s.claim != nil && s.claim.PullRequestNumber == s.event.Number && s.claim.Author == s.release.Author
	deleteOnly := len(s.issue.Assignees) > 1 || (len(s.issue.Assignees) == 1 && !issueHasAssignee(s.issue, s.release.Author)) || (len(s.issue.Assignees) == 0 && (s.issue.State != "open" || (!issueHasLabel(s.issue, claimedLabel) && issueHasLabel(s.issue, helpWantedLabel))))
	if !matching || deleteOnly {
		if matching {
			m.state = prStateReleaseDeleteRecord
			return command{kind: commandDeleteIssueClaim, issue: s.release.Issue, commentID: s.claim.CommentID}, nil
		}
		return m.deleteRecord()
	}
	m.state = prStateReleaseAfterClaim
	return command{kind: commandDeleteIssueClaim, issue: s.release.Issue, commentID: s.claim.CommentID}, nil
}

func (m *prMachine) releaseClaimedLabel() (command, error) {
	if issueHasLabel(m.snapshot.issue, claimedLabel) {
		m.state = prStateReleaseHelpWanted
		return command{kind: commandRemoveIssueLabel, issue: m.snapshot.release.Issue, label: claimedLabel}, nil
	}
	return m.deleteRecord()
}

func (m *prMachine) deleteRecord() (command, error) {
	m.state = prStateDeleteRecord
	return command{kind: commandDeleteClaimRecord, pullRequestNumber: m.snapshot.event.Number, commentID: m.snapshot.release.CommentID}, nil
}

func (m *prMachine) afterRelease() (command, error) {
	s := &m.snapshot
	s.record = nil
	if s.afterRelease == prStateEnsureRecord {
		m.state = prStateAssignIssue
		return command{kind: commandCreateClaimRecord, pullRequestNumber: s.event.Number, issue: s.approved, username: s.pullRequest.Author.Login}, nil
	}
	if s.afterRelease == prStateGateReadPullRequest {
		return m.startGate()
	}
	if s.afterRelease == prStateMilestoneRead {
		return m.startMilestone()
	}
	if s.afterRelease == prStateCloseComment {
		m.state = prStateCloseComment
		return command{kind: commandCreateComment, pullRequestNumber: s.event.Number, comment: fmt.Sprintf("Thanks for the PR, @%s! Issue %s has already been claimed by @%s, so this pull request is closed. Please check other issues labeled `help wanted`.\n\n%s", s.pullRequest.Author.Login, s.approved, s.claimant, gateMarker)}, nil
	}
	return m.done()
}

func (m *prMachine) startMilestone() (command, error) {
	if m.snapshot.approved.Repository != m.snapshot.repository {
		return m.completeClaim()
	}
	m.state = prStateMilestoneRead
	return command{kind: commandReadIssueMilestone, issue: m.snapshot.approved}, nil
}

func (m *prMachine) completeClaim() (command, error) {
	m.state = prStateCompleteLabel
	return command{kind: commandEnsureClaimedLabel}, nil
}

func runPR(owner, repository string, data []byte, adapter *githubAdapter) error {
	event, err := parsePullRequestEvent(data)
	if err != nil {
		return err
	}
	machine := newPRMachine(owner, repository, event)
	result := commandResult{}
	for {
		current, err := machine.step(result)
		if err != nil {
			failed := result.failed
			return fmt.Errorf("state %s command %s for pull request #%d issue %s: %w", stateName(machine.state), commandName(failed.kind), event.Number, failed.issue, err)
		}
		if current.kind == commandDone {
			return nil
		}
		if current.kind == commandWait {
			time.Sleep(current.delay)
			result = commandResult{}
			continue
		}
		result, err = adapter.execute(owner, repository, current)
		if err != nil {
			result.err = err
			result.failed = current
		}
	}
}

func containsIssue(issues []linkedIssue, owner, repository string, reference issueReference) bool {
	for _, current := range issues {
		if current.Owner == owner && current.Repository == repository && current.Number == reference.Number {
			return true
		}
	}
	return false
}

func stateName(state prState) string { return fmt.Sprintf("pr-state-%d", state) }

func commandName(kind commandKind) string {
	names := map[commandKind]string{
		commandReadPullRequest: "read pull request", commandListLinkedIssues: "list linked issues", commandReadIssueMilestone: "read issue milestone",
		commandSetPullRequestMilestone: "set pull request milestone", commandRemoveIssueMilestone: "remove issue milestone", commandWait: "wait",
		commandReadClaimRecord: "read claim record", commandCreateClaimRecord: "create claim record", commandDeleteClaimRecord: "delete claim record",
		commandCreateComment: "create comment", commandSetPullRequestState: "set pull request state", commandReadIssue: "read issue", commandReadIssueClaim: "read issue claim",
		commandCreateIssueClaim: "create issue claim", commandDeleteIssueClaim: "delete issue claim", commandAddIssueAssignee: "add issue assignee", commandRemoveIssueAssignee: "remove issue assignee",
		commandAddIssueLabel: "add issue label", commandRemoveIssueLabel: "remove issue label", commandEnsureClaimedLabel: "ensure claimed label", commandReadLatestAssignment: "read latest assignment",
		commandRestoreHelpWanted: "restore help wanted", commandReadRepositoryPermission: "read repository permission", commandReadRepositoryRole: "read repository role",
		commandReadMergeQueueEnqueuer: "read merge queue enqueuer", commandCancelOtherRuns: "cancel other runs",
	}
	if name, ok := names[kind]; ok {
		return name
	}
	return fmt.Sprintf("command-%d", kind)
}
