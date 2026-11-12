package main

import (
	"fmt"
)

type issueState uint8

const (
	issueStateStart issueState = iota
	issueStatePlatform
	issueStatePermission
	issueStateTriage
	issueStateAssignedRead
	issueStateAssignedClaim
	issueStateAssignedHelpWanted
	issueStateAssignedClaimed
	issueStateDone
)

type issueSnapshot struct {
	event      issueEvent
	owner      string
	repository string
	platforms  []string
	index      int
	permission string
	current    *issue
	claim      *claim
}

type issueMachine struct {
	state    issueState
	snapshot issueSnapshot
}

func newIssueMachine(owner, repository string, event issueEvent) *issueMachine {
	return &issueMachine{state: issueStateStart, snapshot: issueSnapshot{owner: owner, repository: repository, event: event}}
}

func (m *issueMachine) step(result commandResult) (command, error) {
	if result.err != nil {
		return command{}, result.err
	}
	s := &m.snapshot
	switch m.state {
	case issueStateStart:
		switch s.event.Action {
		case "opened":
			if s.event.Issue.Number == 0 {
				return m.done()
			}
			s.platforms = issuePlatforms(s.event.Issue.Body)
			s.index = 0
			m.state = issueStatePlatform
			return m.nextPlatform()
		case "assigned":
			if s.event.Issue.Number == 0 {
				return m.done()
			}
			m.state = issueStateAssignedRead
			return command{kind: commandReadIssue, issue: issueReference{Repository: s.repository, Number: s.event.Issue.Number}}, nil
		default:
			return m.done()
		}
	case issueStatePlatform:
		return m.nextPlatform()
	case issueStatePermission:
		s.permission = result.permission
		if s.event.Issue.User.Type == "Bot" || s.event.Issue.AuthorAssociation == "OWNER" || s.event.Issue.AuthorAssociation == "MEMBER" || s.event.Issue.AuthorAssociation == "COLLABORATOR" || s.permission == "admin" || s.permission == "write" {
			return m.done()
		}
		m.state = issueStateTriage
		return command{kind: commandAddIssueLabel, issue: issueReference{Repository: s.repository, Number: s.event.Issue.Number}, label: "needs triage"}, nil
	case issueStateTriage:
		return m.done()
	case issueStateAssignedRead:
		s.current = result.issue
		if s.current == nil || len(s.current.Assignees) == 0 {
			return m.done()
		}
		m.state = issueStateAssignedClaim
		return command{kind: commandReadIssueClaim, issue: issueReference{Repository: s.repository, Number: s.event.Issue.Number}}, nil
	case issueStateAssignedClaim:
		s.claim = result.claim
		m.state = issueStateAssignedHelpWanted
		if !issueHasLabel(s.current, helpWantedLabel) {
			return m.nextAssignedCleanup()
		}
		return command{kind: commandRemoveIssueLabel, issue: issueReference{Repository: s.repository, Number: s.event.Issue.Number}, label: helpWantedLabel}, nil
	case issueStateAssignedHelpWanted:
		return m.nextAssignedCleanup()
	case issueStateAssignedClaimed:
		return m.done()
	case issueStateDone:
		return command{kind: commandDone}, nil
	default:
		return command{}, fmt.Errorf("unknown issue state %d", m.state)
	}
}

func (m *issueMachine) nextAssignedCleanup() (command, error) {
	s := &m.snapshot
	if claimOwnershipFor(s.current, s.claim, "", 0) == claimOwnershipSuperseded && issueHasLabel(s.current, claimedLabel) {
		m.state = issueStateAssignedClaimed
		return command{kind: commandRemoveIssueLabel, issue: issueReference{Repository: s.repository, Number: s.event.Issue.Number}, label: claimedLabel}, nil
	}
	return m.done()
}

func (m *issueMachine) nextPlatform() (command, error) {
	s := &m.snapshot
	if s.index < len(s.platforms) {
		label := s.platforms[s.index]
		s.index++
		return command{kind: commandAddIssueLabel, issue: issueReference{Repository: s.repository, Number: s.event.Issue.Number}, label: label}, nil
	}
	m.state = issueStatePermission
	return command{kind: commandReadRepositoryPermission, username: s.event.Issue.User.Login}, nil
}

func (m *issueMachine) done() (command, error) {
	m.state = issueStateDone
	return command{kind: commandDone}, nil
}

func runIssue(owner, repository string, data []byte, adapter *githubAdapter) error {
	event, err := parseIssueEvent(data)
	if err != nil {
		return err
	}
	machine := newIssueMachine(owner, repository, event)
	result := commandResult{}
	for {
		current, err := machine.step(result)
		if err != nil {
			return fmt.Errorf("state %s command %s for issue #%d: %w", issueStateName(machine.state), commandName(result.failed.kind), event.Issue.Number, err)
		}
		if current.kind == commandDone {
			return nil
		}
		result, err = adapter.execute(owner, repository, current)
		if err != nil {
			result.err = err
			result.failed = current
		}
	}
}

func issueStateName(state issueState) string { return fmt.Sprintf("issue-state-%d", state) }
