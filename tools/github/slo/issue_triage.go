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
	for _, event := range events {
		if event.GetEvent() != "unlabeled" || event.GetLabel().GetName() != needsTriageLabel {
			continue
		}
		maintainer, err := s.isMaintainer(ctx, event.GetActor().GetLogin())
		if err != nil {
			return false, err
		}
		if maintainer {
			return true, nil
		}
	}
	return false, nil
}

func issueHasLabel(issue *github.Issue, name string) bool {
	for _, label := range issue.Labels {
		if label.GetName() == name {
			return true
		}
	}
	return false
}
