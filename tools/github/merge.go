package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var mergeQueueRef = regexp.MustCompile(`/pr-(\d+)-[0-9a-f]+$`)

var ErrNotAllowed = errors.New("queued by someone without the admin or maintain role")

type mergeState uint8

const (
	mergeStateStart mergeState = iota
	mergeStateEnqueuer
	mergeStateRole
	mergeStateComment
	mergeStateCancel
	mergeStateDone
)

type mergeSnapshot struct {
	event    mergeGroupEvent
	prNumber int
	enqueuer string
	role     string
	runID    int64
}

type mergeMachine struct {
	state    mergeState
	snapshot mergeSnapshot
}

func newMergeMachine(event mergeGroupEvent, runID int64) *mergeMachine {
	return &mergeMachine{state: mergeStateStart, snapshot: mergeSnapshot{event: event, runID: runID}}
}

func (m *mergeMachine) step(result commandResult) (command, error) {
	if result.err != nil {
		return command{}, result.err
	}
	s := &m.snapshot
	switch m.state {
	case mergeStateStart:
		match := mergeQueueRef.FindStringSubmatch(s.event.HeadRef)
		if match == nil {
			return command{}, fmt.Errorf("not a merge queue ref: %q", s.event.HeadRef)
		}
		var err error
		s.prNumber, err = strconv.Atoi(match[1])
		if err != nil {
			return command{}, err
		}
		m.state = mergeStateEnqueuer
		return command{kind: commandReadMergeQueueEnqueuer, pullRequestNumber: s.prNumber}, nil
	case mergeStateEnqueuer:
		s.enqueuer = result.enqueuer
		m.state = mergeStateRole
		return command{kind: commandReadRepositoryRole, username: s.enqueuer}, nil
	case mergeStateRole:
		s.role = result.role
		if s.role == "admin" || s.role == "maintain" {
			return m.done()
		}
		m.state = mergeStateComment
		return command{kind: commandCreateComment, pullRequestNumber: s.prNumber, comment: fmt.Sprintf("Only a repository admin or maintainer can merge, so this was taken out of the merge queue (queued by @%s). A maintainer will merge it.", s.enqueuer)}, nil
	case mergeStateComment:
		m.state = mergeStateCancel
		return command{kind: commandCancelOtherRuns, headSHA: s.event.HeadSHA, runID: s.runID}, nil
	case mergeStateCancel:
		return command{}, fmt.Errorf("%w: @%s has the %q role", ErrNotAllowed, s.enqueuer, s.role)
	case mergeStateDone:
		return command{kind: commandDone}, nil
	default:
		return command{}, fmt.Errorf("unknown merge state %d", m.state)
	}
}

func (m *mergeMachine) done() (command, error) {
	m.state = mergeStateDone
	return command{kind: commandDone}, nil
}

func runMerge(owner, repository string, data []byte, adapter *githubAdapter) error {
	event, err := parseMergeGroupEvent(data)
	if err != nil {
		return err
	}
	runID, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	machine := newMergeMachine(event, runID)
	result := commandResult{}
	for {
		current, err := machine.step(result)
		if err != nil {
			return fmt.Errorf("state %s command %s for pull request #%d merge-group %s: %w", mergeStateName(machine.state), commandName(result.failed.kind), machine.snapshot.prNumber, event.HeadSHA, err)
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

func mergeStateName(state mergeState) string { return fmt.Sprintf("merge-state-%d", state) }
