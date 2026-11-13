package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

const maxMilestoneRemovals = 3

func Milestone(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	event, err := parsePullRequestEvent(eventData)
	if err != nil {
		return fmt.Errorf("parse pull request event: %w", err)
	}
	if event.number == 0 {
		return nil
	}
	pullRequest, err := readPullRequest(ctx, client, owner, repository, event.number)
	if err != nil || pullRequest == nil || pullRequest.State != "open" || pullRequest.BaseRef != "main" {
		return err
	}
	record, err := readClaimRecord(ctx, client, owner, repository, event.number)
	if err != nil || record == nil || record.Issue.Repository != repository {
		return err
	}
	issue, _, err := client.Issues.Get(ctx, owner, record.Issue.Repository, record.Issue.Number)
	if contribution.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read issue %s for milestone: %w", record.Issue, err)
	}
	if issue == nil || issue.Milestone == nil {
		return nil
	}
	target, err := readPullRequestIssue(ctx, client, owner, repository, event.number)
	if err != nil || target == nil {
		return err
	}
	milestone := issue.Milestone.GetNumber()
	if pullRequestMilestone(target) != milestone {
		if _, _, err := client.Issues.Update(ctx, owner, repository, event.number, github.UpdateIssueRequest{Milestone: &milestone}); err != nil {
			return fmt.Errorf("set milestone on pull request #%d: %w", event.number, err)
		}
	}
	for attempt := 0; attempt < maxMilestoneRemovals; attempt++ {
		_, _, removeErr := client.Issues.RemoveMilestone(ctx, owner, record.Issue.Repository, record.Issue.Number)
		if removeErr == nil || contribution.IsNotFound(removeErr) {
			return nil
		}
		if attempt == maxMilestoneRemovals-1 {
			return fmt.Errorf("remove milestone from issue %s: %w", record.Issue, removeErr)
		}
		if err := wait(ctx, gateRetryDelay); err != nil {
			return err
		}
	}
	return nil
}

func pullRequestMilestone(item *github.Issue) int {
	if item == nil || item.Milestone == nil {
		return 0
	}
	return item.Milestone.GetNumber()
}
