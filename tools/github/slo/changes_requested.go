package slo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v93/github"
)

func (s *state) planRequestedChanges(ctx context.Context, pull *github.PullRequest, reviews []*github.PullRequestReview, events []*github.IssueEvent, markers []marker) error {
	change := latestChangesRequest(reviews)
	if change == nil {
		return nil
	}
	maintainer, err := s.isMaintainer(ctx, change.GetUser().GetLogin())
	if err != nil {
		return err
	}
	if !maintainer {
		return nil
	}
	commits, err := listCommits(ctx, s.client, s.owner, s.repository, pull.GetNumber())
	if err != nil {
		return fmt.Errorf("list commits for pull request #%d: %w", pull.GetNumber(), err)
	}
	fixedAt := contributorFixTime(commits, pull.GetUser().GetLogin(), change.GetSubmittedAt().Time)
	if !fixedAt.IsZero() && contributorRequestedReview(events, pull.GetUser().GetLogin(), fixedAt) {
		return nil
	}
	s.planReminder(changesObjective, pull.GetNumber(), change.GetSubmittedAt().Time, pull.GetUser().GetLogin(), markers)
	return nil
}

func latestChangesRequest(reviews []*github.PullRequestReview) *github.PullRequestReview {
	var latest *github.PullRequestReview
	for _, review := range reviews {
		if review.GetState() == "CHANGES_REQUESTED" && !review.GetSubmittedAt().Time.IsZero() &&
			(latest == nil || review.GetSubmittedAt().After(latest.GetSubmittedAt().Time)) {
			latest = review
		}
	}
	return latest
}

func contributorFixTime(commits []*github.RepositoryCommit, contributor string, after time.Time) time.Time {
	var latest time.Time
	for _, commit := range commits {
		date := commit.GetCommit().GetAuthor().GetDate().Time
		if commit.GetAuthor().GetLogin() == contributor && date.After(after) && date.After(latest) {
			latest = date
		}
	}
	return latest
}

func contributorRequestedReview(events []*github.IssueEvent, contributor string, after time.Time) bool {
	for _, event := range events {
		if event.GetEvent() == "review_requested" && event.GetActor().GetLogin() == contributor && event.GetCreatedAt().After(after) {
			return true
		}
	}
	return false
}
