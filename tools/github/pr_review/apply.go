package pr_review

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

type changeKind string

const (
	addLabel    changeKind = "add-label"
	removeLabel changeKind = "remove-label"
)

type change struct {
	kind  changeKind
	label string
}

func (s *state) apply(ctx context.Context) error {
	for _, change := range s.changes {
		switch change.kind {
		case addLabel:
			if err := s.ensureLabel(ctx, change.label); err != nil {
				return err
			}
			if _, _, err := s.client.Issues.AddLabelsToIssue(ctx, s.owner, s.repository, s.event.number, []string{change.label}); err != nil {
				return fmt.Errorf("add %q label to pull request #%d: %w", change.label, s.event.number, err)
			}
		case removeLabel:
			if _, err := s.client.Issues.RemoveLabelForIssue(ctx, s.owner, s.repository, s.event.number, change.label); err != nil {
				return fmt.Errorf("remove %q label from pull request #%d: %w", change.label, s.event.number, err)
			}
		default:
			return fmt.Errorf("unknown review label change %q", change.kind)
		}
	}
	return nil
}

func (s *state) ensureLabel(ctx context.Context, name string) error {
	_, _, err := s.client.Issues.GetLabel(ctx, s.owner, s.repository, name)
	if err == nil {
		return nil
	}
	if !contribution.IsNotFound(err) {
		return fmt.Errorf("read %q label in repository %s/%s: %w", name, s.owner, s.repository, err)
	}
	color, description := reviewLabelPresentation(name)
	_, _, err = s.client.Issues.CreateLabel(ctx, s.owner, s.repository, github.CreateIssueLabelRequest{
		Name: name, Color: &color, Description: &description,
	})
	if err != nil {
		return fmt.Errorf("create %q label in repository %s/%s: %w", name, s.owner, s.repository, err)
	}
	return nil
}

func reviewLabelPresentation(name string) (string, string) {
	switch name {
	case changesRequestedLabel:
		return "d93f0b", "A maintainer requested changes"
	case reviewRequestedLabel:
		return "fbca04", "Waiting for a maintainer review"
	default:
		return "ededed", name
	}
}
