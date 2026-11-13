package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func Release(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	event, err := parsePullRequestEvent(eventData)
	if err != nil {
		return fmt.Errorf("parse pull request event: %w", err)
	}
	if event.number == 0 {
		return nil
	}
	pullRequest, err := readPullRequest(ctx, client, owner, repository, event.number)
	if err != nil || pullRequest == nil || pullRequest.Merged {
		return err
	}
	record, err := readClaimRecord(ctx, client, owner, repository, event.number)
	if err != nil || record == nil {
		return err
	}
	linked, err := listClosingReferences(ctx, client, owner, repository, event.number)
	if err != nil {
		return err
	}
	if pullRequest.State == "open" && pullRequest.BaseRef == "main" && containsReference(linked, owner, record.Issue) {
		return nil
	}
	claims, err := readIssueClaims(ctx, client, owner, record.Issue)
	if err != nil && !contribution.IsNotFound(err) {
		return err
	}
	for _, claim := range claims {
		if claim.PullRequestNumber != event.number {
			continue
		}
		if err := deleteComment(ctx, client, owner, record.Issue.Repository, claim.CommentID); err != nil &&
			!contribution.IsNotFound(err) {
			return fmt.Errorf("delete issue claim for %s: %w", record.Issue, err)
		}
	}
	issue, err := readIssue(ctx, client, owner, record.Issue)
	if err != nil {
		return err
	}
	if issue == nil {
		return deleteClaimRecord(ctx, client, owner, repository, event.number, record)
	}
	if issue.HasLabel(contribution.ClaimedLabel) {
		if _, err := client.Issues.RemoveLabelForIssue(ctx, owner, record.Issue.Repository, record.Issue.Number, contribution.ClaimedLabel); err != nil &&
			!contribution.IsNotFound(err) {
			return fmt.Errorf("remove %q label from issue %s: %w", contribution.ClaimedLabel, record.Issue, err)
		}
	}
	if contribution.OtherAssignee(issue, record.Author) {
		return deleteClaimRecord(ctx, client, owner, repository, event.number, record)
	}
	assigner, assigned, err := readLatestAssignment(ctx, client, owner, record.Issue, record.Author)
	if err != nil {
		return err
	}
	if !assigned || assigner != "github-actions[bot]" {
		return deleteClaimRecord(ctx, client, owner, repository, event.number, record)
	}
	if _, _, err := client.Issues.RemoveAssignees(ctx, owner, record.Issue.Repository, record.Issue.Number, []string{record.Author}); err != nil &&
		!contribution.IsNotFound(err) {
		return fmt.Errorf("remove @%s from issue %s: %w", record.Author, record.Issue, err)
	}
	current, err := readIssue(ctx, client, owner, record.Issue)
	if err != nil {
		return err
	}
	if current != nil && current.State == "open" && len(current.Assignees) == 0 {
		if _, _, err := client.Issues.AddLabelsToIssue(ctx, owner, record.Issue.Repository, record.Issue.Number, []string{contribution.HelpWantedLabel}); err != nil {
			return fmt.Errorf("restore %q label on issue %s: %w", contribution.HelpWantedLabel, record.Issue, err)
		}
	}
	return deleteClaimRecord(ctx, client, owner, repository, event.number, record)
}

func containsReference(issues []contribution.LinkedIssue, owner string, reference contribution.IssueReference) bool {
	for _, issue := range issues {
		if issue.Owner == owner && issue.Repository == reference.Repository && issue.Number == reference.Number {
			return true
		}
	}
	return false
}

func deleteClaimRecord(ctx context.Context, client *github.Client, owner, repository string, pullRequestNumber int, record *contribution.ClaimRecord) error {
	if record == nil || record.CommentID == 0 {
		return nil
	}
	if err := deleteComment(ctx, client, owner, repository, record.CommentID); err != nil &&
		!contribution.IsNotFound(err) {
		return fmt.Errorf("delete claim record for pull request #%d issue %s: %w", pullRequestNumber, record.Issue, err)
	}
	return nil
}
