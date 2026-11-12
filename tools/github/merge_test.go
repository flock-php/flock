package main

import (
	"errors"
	"testing"
)

const mergeTestRef = "refs/heads/gh-readonly-queue/main/pr-123-ce2a51d86b2e1be42ffd4c0d5a020fd0d7270588"

func TestMergeMachineAllowsAdminsAndMaintainers(t *testing.T) {
	for _, role := range []string{"admin", "maintain"} {
		t.Run(role, func(t *testing.T) {
			machine := newMergeMachine(mergeGroupEvent{HeadRef: mergeTestRef, HeadSHA: "abc"}, 7)
			if command, err := machine.step(commandResult{}); err != nil || command.kind != commandReadMergeQueueEnqueuer || command.pullRequestNumber != 123 {
				t.Fatalf("enqueuer command = %+v, %v", command, err)
			}
			if command, err := machine.step(commandResult{enqueuer: "maintainer"}); err != nil || command.kind != commandReadRepositoryRole {
				t.Fatalf("role command = %+v, %v", command, err)
			}
			if command, err := machine.step(commandResult{role: role}); err != nil || command.kind != commandDone {
				t.Fatalf("allow command = %+v, %v", command, err)
			}
		})
	}
}

func TestMergeMachineCommentsBeforeCancellingAndPreservesNotAllowed(t *testing.T) {
	machine := newMergeMachine(mergeGroupEvent{HeadRef: mergeTestRef, HeadSHA: "abc"}, 7)
	_, _ = machine.step(commandResult{})
	_, _ = machine.step(commandResult{enqueuer: "contributor"})
	command, err := machine.step(commandResult{role: "write"})
	if err != nil || command.kind != commandCreateComment {
		t.Fatalf("comment command = %+v, %v", command, err)
	}
	command, err = machine.step(commandResult{})
	if err != nil || command.kind != commandCancelOtherRuns || command.headSHA != "abc" || command.runID != 7 {
		t.Fatalf("cancel command = %+v, %v", command, err)
	}
	_, err = machine.step(commandResult{})
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("error = %v, want ErrNotAllowed", err)
	}
}

func TestMergeMachineRejectsMalformedRefsBeforeSideEffects(t *testing.T) {
	machine := newMergeMachine(mergeGroupEvent{HeadRef: "refs/heads/main"}, 7)
	if _, err := machine.step(commandResult{}); err == nil {
		t.Fatal("malformed ref returned nil")
	}
	machine.state = mergeState(99)
	if _, err := machine.step(commandResult{}); err == nil {
		t.Fatal("unknown state returned nil")
	}
}

func TestMergeMachinePropagatesCommandErrors(t *testing.T) {
	machine := newMergeMachine(mergeGroupEvent{HeadRef: mergeTestRef}, 0)
	want := errors.New("enqueuer")
	_, _ = machine.step(commandResult{})
	if _, err := machine.step(commandResult{err: want}); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
