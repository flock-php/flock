package pr_review

import (
	"strings"

	"flock/tools/github/contribution"
)

func (s *state) plan() {
	if s.event.kind == workflowRunEventKind {
		s.planRequestedChanges()
		return
	}
	pullRequest := s.observed.pullRequest
	if pullRequest == nil || pullRequest.State != "open" || pullRequest.Merged || pullRequest.BaseRef != "main" {
		return
	}
	switch s.event.action {
	case "opened":
		if contribution.BypassesGate(pullRequest) {
			return
		}
	case "review_requested":
		if !contribution.BypassesGate(pullRequest) && !strings.EqualFold(s.event.sender, pullRequest.Author.Login) {
			return
		}
	default:
		return
	}
	if !s.observed.labels.Has(reviewRequestedLabel) {
		s.changes = append(s.changes, change{kind: addLabel, label: reviewRequestedLabel})
	}
	if s.observed.labels.Has(changesRequestedLabel) {
		s.changes = append(s.changes, change{kind: removeLabel, label: changesRequestedLabel})
	}
}

func (s *state) planRequestedChanges() {
	if !s.observed.validMaintainerReview {
		return
	}
	if !s.observed.labels.Has(changesRequestedLabel) {
		s.changes = append(s.changes, change{kind: addLabel, label: changesRequestedLabel})
	}
	if s.observed.labels.Has(reviewRequestedLabel) {
		s.changes = append(s.changes, change{kind: removeLabel, label: reviewRequestedLabel})
	}
}
