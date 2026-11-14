package issue

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func Assignment(ctx context.Context, client *github.Client, owner, repository string, data []byte) error {
	event, err := parseIssueEvent(data)
	if err != nil {
		return fmt.Errorf("parse issue event: %w", err)
	}
	if event.action != "assigned" || event.issue.Number == 0 {
		return nil
	}

	item, _, err := client.Issues.Get(ctx, owner, repository, event.issue.Number)
	if contribution.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read assigned issue #%d: %w", event.issue.Number, err)
	}
	current := contribution.IssueFromGitHub(item)
	if current == nil || len(current.Assignees) == 0 {
		return nil
	}

	claims, err := findIssueClaims(ctx, client, owner, repository, event.issue.Number)
	if err != nil {
		return fmt.Errorf("read claim for issue #%d: %w", event.issue.Number, err)
	}
	if current.HasLabel(contribution.HelpWantedLabel) {
		if err := removeLabel(ctx, client, owner, repository, event.issue.Number, contribution.HelpWantedLabel); err != nil {
			return err
		}
	}
	if current.HasLabel(contribution.ClaimedLabel) && len(claims) != 0 && !contribution.HasAssignedClaimAuthor(current, claims) {
		return removeLabel(ctx, client, owner, repository, event.issue.Number, contribution.ClaimedLabel)
	}
	return nil
}

func findIssueClaims(ctx context.Context, client *github.Client, owner, repository string, number int) ([]contribution.Claim, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var claims []contribution.Claim
	for {
		comments, response, err := client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		claims = append(claims, contribution.ClaimsFromComments(comments)...)
		if response.NextPage == 0 {
			return claims, nil
		}
		options.Page = response.NextPage
	}
}

func removeLabel(ctx context.Context, client *github.Client, owner, repository string, number int, label string) error {
	if _, err := client.Issues.RemoveLabelForIssue(ctx, owner, repository, number, label); err != nil &&
		!contribution.IsNotFound(err) {
		return fmt.Errorf("remove %q label from issue #%d: %w", label, number, err)
	}
	return nil
}
