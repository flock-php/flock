package main

import (
	"encoding/json"
	"testing"

	"github.com/google/go-github/v93/github"
)

func TestPullRequestEventWithoutChangesDoesNotPanic(t *testing.T) {
	var event github.PullRequestEvent
	if err := json.Unmarshal([]byte(`{"action":"opened","pull_request":{"number":87}}`), &event); err != nil {
		t.Fatal(err)
	}

	gateEvent := pullRequestGateEvent(&event)
	if gateEvent.BodyEdited {
		t.Fatal("BodyEdited = true, want false")
	}
	if gateEvent.BaseEdited {
		t.Fatal("BaseEdited = true, want false")
	}
}
