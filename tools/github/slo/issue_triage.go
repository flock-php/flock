package slo

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func (s *state) collectIssues(ctx context.Context) error {
	issues, err := listOpenIssues(ctx, s.client, s.owner, s.repository)
	if err != nil {
		return err
	}
	for _, issue := range issues {
		if issue.GetPullRequestLinks() != nil || !contribution.IsOutsideContributor(issue.GetUser().GetType(), issue.GetAuthorAssociation()) {
			continue
		}
		if err := s.planIssueTriage(ctx, issue); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) planIssueTriage(ctx context.Context, issue *github.Issue) error {
	events, err := listIssueEvents(ctx, s.client, s.owner, s.repository, issue.GetNumber())
	if err != nil {
		return fmt.Errorf("list events for issue #%d: %w", issue.GetNumber(), err)
	}
	triaged, err := s.issueTriaged(ctx, issue, events)
	if err != nil {
		return fmt.Errorf("check triage for issue #%d: %w", issue.GetNumber(), err)
	}
	if triaged {
		return nil
	}
	comments, err := listComments(ctx, s.client, s.owner, s.repository, issue.GetNumber())
	if err != nil {
		return fmt.Errorf("list comments for issue #%d: %w", issue.GetNumber(), err)
	}
	s.planReminder(issueTriageObjective, issue.GetNumber(), issue.GetCreatedAt().Time, "", "", remindersFrom(comments))
	return nil
}

const needsTriageLabel = "needs triage"

func (s *state) issueTriaged(ctx context.Context, issue *github.Issue, events []*github.IssueEvent) (bool, error) {
	if issueHasLabel(issue, needsTriageLabel) {
		return false, nil
	}
	var removal *github.IssueEvent
	for _, event := range events {
		if event.GetEvent() != "unlabeled" || event.GetLabel().GetName() != needsTriageLabel {
			continue
		}
		if removal == nil || !event.GetCreatedAt().Time.Before(removal.GetCreatedAt().Time) {
			removal = event
		}
	}
	// No removal means the issue is not waiting on needs triage.
	if removal == nil {
		return true, nil
	}
	return s.isMaintainer(ctx, removal.GetActor().GetLogin())
}

func issueHasLabel(issue *github.Issue, name string) bool {
	for _, label := range issue.Labels {
		if label.GetName() == name {
			return true
		}
	}
	return false
}
