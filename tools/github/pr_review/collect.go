package pr_review

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func (s *state) collect(ctx context.Context) error {
	if !s.event.valid {
		return nil
	}
	if s.event.kind == workflowRunEventKind {
		number, err := s.resolveWorkflowPullRequest(ctx)
		if err != nil {
			return err
		}
		// The artifact names a review. It does not choose which pull request receives the label.
		if number < 1 || number != s.event.review.number {
			return nil
		}
		s.event.number = number
	}
	pullRequest, _, err := s.client.PullRequests.Get(ctx, s.owner, s.repository, s.event.number)
	if contribution.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pull request #%d: %w", s.event.number, err)
	}
	s.observed.pullRequest = contribution.PullRequestFromGitHub(pullRequest)
	s.observed.labels = make(labelSet, len(s.observed.pullRequest.Labels))
	for _, label := range s.observed.pullRequest.Labels {
		s.observed.labels[label.Name] = struct{}{}
	}
	if s.event.kind == workflowRunEventKind {
		return s.collectRequestedChanges(ctx, pullRequest)
	}
	return nil
}

func (s *state) collectRequestedChanges(ctx context.Context, pullRequest *github.PullRequest) error {
	if pullRequest == nil || pullRequest.GetState() != "open" || pullRequest.GetMerged() ||
		pullRequest.GetBase().GetRef() != "main" || pullRequest.GetHead().GetSHA() == "" {
		return nil
	}
	review, _, err := s.client.PullRequests.GetReview(ctx, s.owner, s.repository, s.event.number, s.event.review.reviewID)
	if contribution.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read review %d for pull request #%d: %w", s.event.review.reviewID, s.event.number, err)
	}
	// workflow_run.head_sha is the merge commit, not the pull request head. The artifact commit id is untrusted.
	if review == nil || review.GetID() != s.event.review.reviewID ||
		!strings.EqualFold(review.GetState(), "changes_requested") ||
		review.GetSubmittedAt().IsZero() || review.GetUser().GetLogin() == "" ||
		review.GetCommitID() == "" || review.GetCommitID() != pullRequest.GetHead().GetSHA() {
		return nil
	}
	permission, _, err := s.client.Repositories.GetPermissionLevel(ctx, s.owner, s.repository, review.GetUser().GetLogin())
	if contribution.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read repository role for review author @%s: %w", review.GetUser().GetLogin(), err)
	}
	if permission == nil || (permission.GetRoleName() != "admin" && permission.GetRoleName() != "maintain") {
		return nil
	}
	stale, err := s.hasLaterBotLabelTransition(ctx, review.GetSubmittedAt().Time)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	s.observed.validMaintainerReview = true
	return nil
}

func (s *state) resolveWorkflowPullRequest(ctx context.Context) (int, error) {
	if s.event.workflow.headOwner != "" && s.event.workflow.headBranch != "" {
		return s.pullRequestForHead(ctx)
	}
	return s.event.workflow.pullRequest, nil
}

func (s *state) pullRequestForHead(ctx context.Context) (int, error) {
	head := s.event.workflow.headOwner + ":" + s.event.workflow.headBranch
	options := &github.PullRequestListOptions{
		State:       "open",
		Head:        head,
		Base:        "main",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var matches []int
	for {
		pulls, response, err := s.client.PullRequests.List(ctx, s.owner, s.repository, options)
		if err != nil {
			return 0, fmt.Errorf("list pull requests for %s: %w", head, err)
		}
		for _, pull := range pulls {
			if pull.GetNumber() < 1 || pull.GetState() != "open" || pull.GetBase().GetRef() != "main" ||
				pull.GetHead().GetRef() != s.event.workflow.headBranch ||
				pull.GetHead().GetRepo().GetOwner().GetLogin() != s.event.workflow.headOwner {
				continue
			}
			matches = append(matches, pull.GetNumber())
		}
		if response == nil || response.NextPage == 0 {
			break
		}
		if options.Page == response.NextPage {
			return 0, fmt.Errorf("list pull requests for %s: page did not advance", head)
		}
		options.Page = response.NextPage
	}
	if len(matches) != 1 {
		return 0, nil
	}
	if s.event.workflow.pullRequest > 0 && matches[0] != s.event.workflow.pullRequest {
		return 0, nil
	}
	return matches[0], nil
}

func (s *state) hasLaterBotLabelTransition(ctx context.Context, submittedAt time.Time) (bool, error) {
	options := &github.ListOptions{PerPage: 100}
	for {
		events, response, err := s.client.Issues.ListIssueEvents(ctx, s.owner, s.repository, s.event.number, options)
		if err != nil {
			return false, fmt.Errorf("list label events for pull request #%d: %w", s.event.number, err)
		}
		for _, event := range events {
			if event.GetEvent() != "labeled" || event.GetActor().GetLogin() != "github-actions[bot]" ||
				event.GetCreatedAt().Time.IsZero() || !event.GetCreatedAt().Time.After(submittedAt) {
				continue
			}
			label := event.GetLabel().GetName()
			if label == reviewRequestedLabel || label == changesRequestedLabel {
				return true, nil
			}
		}
		if response == nil || response.NextPage == 0 {
			return false, nil
		}
		if options.Page == response.NextPage {
			return false, fmt.Errorf("list label events for pull request #%d: page did not advance", s.event.number)
		}
		options.Page = response.NextPage
	}
}
