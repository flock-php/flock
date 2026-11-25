package pr_review

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

const (
	reviewRequestedLabel  = "pr: review requested"
	changesRequestedLabel = "pr: changes requested"
	signalWorkflowName    = "PR Review Signal"
)

type state struct {
	client     *github.Client
	owner      string
	repository string
	event      event

	observed observed
	changes  []change
}

type observed struct {
	pullRequest           *contribution.PullRequest
	labels                labelSet
	validMaintainerReview bool
}

type labelSet map[string]struct{}

func (labels labelSet) Has(name string) bool {
	_, ok := labels[name]
	return ok
}

func Run(ctx context.Context, client *github.Client, owner, repository string, eventData []byte, signalPath string) error {
	current, err := newState(client, owner, repository, eventData, signalPath)
	if err != nil {
		return err
	}
	if err := current.collect(ctx); err != nil {
		return err
	}
	current.plan()
	return current.apply(ctx)
}

func newState(client *github.Client, owner, repository string, eventData []byte, signalPath string) (*state, error) {
	currentEvent, err := parseEvent(eventData, signalPath)
	if err != nil {
		return nil, fmt.Errorf("parse pull request review event: %w", err)
	}
	return &state{
		client:     client,
		owner:      owner,
		repository: repository,
		event:      currentEvent,
	}, nil
}
