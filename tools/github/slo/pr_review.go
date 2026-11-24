package slo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func (s *state) collectPullRequests(ctx context.Context) error {
	pulls, err := listOpenPullRequests(ctx, s.client, s.owner, s.repository)
	if err != nil {
		return err
	}
	for _, pull := range pulls {
		if !contribution.IsOutsideContributor(pull.GetUser().GetType(), pull.GetAuthorAssociation()) {
			continue
		}
		if err := s.planPullRequestTargets(ctx, pull); err != nil {
			return err
		}
	}
	return nil
}

func (s *state) planPullRequestTargets(ctx context.Context, pull *github.PullRequest) error {
	number := pull.GetNumber()
	reviews, err := listReviews(ctx, s.client, s.owner, s.repository, number)
	if err != nil {
		return fmt.Errorf("list reviews for pull request #%d: %w", number, err)
	}
	events, err := listIssueEvents(ctx, s.client, s.owner, s.repository, number)
	if err != nil {
		return fmt.Errorf("list events for pull request #%d: %w", number, err)
	}
	comments, err := listComments(ctx, s.client, s.owner, s.repository, number)
	if err != nil {
		return fmt.Errorf("list comments for pull request #%d: %w", number, err)
	}
	markers := remindersFrom(comments)

	if err := s.planPRReview(ctx, pull, reviews, markers); err != nil {
		return err
	}
	if err := s.planMergeReview(ctx, pull, reviews, events, markers); err != nil {
		return err
	}
	return s.planRequestedChanges(ctx, pull, reviews, events, markers)
}

func (s *state) planPRReview(ctx context.Context, pull *github.PullRequest, reviews []*github.PullRequestReview, markers []marker) error {
	for _, review := range reviews {
		if !submittedReview(review) {
			continue
		}
		maintainer, err := s.isMaintainer(ctx, review.GetUser().GetLogin())
		if err != nil {
			return fmt.Errorf("check reviews for pull request #%d: %w", pull.GetNumber(), err)
		}
		if maintainer {
			return nil
		}
	}
	s.planReminder(prReviewObjective, pull.GetNumber(), pull.GetCreatedAt().Time, "", markers)
	return nil
}

func submittedReview(review *github.PullRequestReview) bool {
	return !review.GetSubmittedAt().Time.IsZero() &&
		(review.GetState() == "COMMENTED" || review.GetState() == "APPROVED" || review.GetState() == "CHANGES_REQUESTED")
}

func (s *state) planMergeReview(ctx context.Context, pull *github.PullRequest, reviews []*github.PullRequestReview, events []*github.IssueEvent, markers []marker) error {
	for _, approval := range latestApprovals(reviews) {
		approver := approval.GetUser().GetLogin()
		mergeMaintainer, err := s.isMaintainer(ctx, approver)
		if err != nil {
			return err
		}
		if mergeMaintainer {
			continue
		}
		for _, event := range events {
			if event.GetEvent() != "review_requested" || event.GetActor().GetLogin() != approver ||
				!event.GetCreatedAt().After(approval.GetSubmittedAt().Time) {
				continue
			}
			target := event.GetRequestedReviewer().GetLogin()
			eligible, err := s.isMaintainer(ctx, target)
			if err != nil {
				return err
			}
			if !eligible || reviewRequestResolved(target, event.GetCreatedAt().Time, reviews, events) {
				continue
			}
			s.planReminder(mergeReviewObjective, pull.GetNumber(), event.GetCreatedAt().Time, target, markers)
		}
	}
	return nil
}

func latestApprovals(reviews []*github.PullRequestReview) []*github.PullRequestReview {
	latest := map[string]*github.PullRequestReview{}
	for _, review := range reviews {
		if !submittedReview(review) {
			continue
		}
		login := review.GetUser().GetLogin()
		if current := latest[login]; current == nil || review.GetSubmittedAt().After(current.GetSubmittedAt().Time) {
			latest[login] = review
		}
	}
	var approvals []*github.PullRequestReview
	for _, review := range latest {
		if review.GetState() == "APPROVED" {
			approvals = append(approvals, review)
		}
	}
	sort.Slice(approvals, func(i, j int) bool {
		return approvals[i].GetSubmittedAt().Before(approvals[j].GetSubmittedAt().Time)
	})
	return approvals
}

func reviewRequestResolved(target string, start time.Time, reviews []*github.PullRequestReview, events []*github.IssueEvent) bool {
	for _, review := range reviews {
		if review.GetUser().GetLogin() == target && submittedReview(review) && review.GetSubmittedAt().After(start) {
			return true
		}
	}
	for _, event := range events {
		if event.GetEvent() == "review_request_removed" && event.GetRequestedReviewer().GetLogin() == target &&
			event.GetCreatedAt().After(start) {
			return true
		}
	}
	return false
}
