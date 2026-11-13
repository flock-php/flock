package pr

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

func Claim(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
	event, err := parsePullRequestEvent(eventData)
	if err != nil {
		return fmt.Errorf("parse pull request event: %w", err)
	}
	if event.number == 0 {
		return nil
	}
	pullRequest, err := readPullRequest(ctx, client, owner, repository, event.number)
	if err != nil || pullRequest == nil {
		return err
	}
	if pullRequest.State == "closed" || pullRequest.Merged || pullRequest.BaseRef != "main" || bypassesGate(pullRequest) {
		return nil
	}
	linked, err := listClosingReferences(ctx, client, owner, repository, event.number)
	if err != nil {
		return err
	}
	approved, _, err := firstApproved(ctx, client, owner, repository, pullRequest.Author.Login, event.number, linked)
	if err != nil || approved == nil {
		return err
	}
	claims, err := readIssueClaims(ctx, client, owner, *approved)
	if err != nil {
		return err
	}
	hasPullRequestClaim := false
	for _, claim := range claims {
		if claim.PullRequestNumber == event.number {
			hasPullRequestClaim = true
			break
		}
	}
	record, err := readClaimRecord(ctx, client, owner, repository, event.number)
	if err != nil {
		return err
	}
	if record == nil {
		if err := createClaimRecord(ctx, client, owner, repository, event.number, *approved, pullRequest.Author.Login); err != nil {
			return err
		}
	}
	if !hasPullRequestClaim {
		if err := createIssueClaim(ctx, client, owner, *approved, event.number, pullRequest.Author.Login); err != nil {
			return err
		}
	}
	issue, err := readIssue(ctx, client, owner, *approved)
	if err != nil {
		return err
	}
	if issue == nil {
		return nil
	}
	if err := ensureClaimedLabel(ctx, client, owner, repository); err != nil {
		return err
	}
	if !issue.HasAssignee(pullRequest.Author.Login) {
		item, _, err := client.Issues.AddAssignees(ctx, owner, approved.Repository, approved.Number, []string{pullRequest.Author.Login})
		if err != nil {
			return fmt.Errorf("assign issue %s to @%s: %w", *approved, pullRequest.Author.Login, err)
		}
		issue = contribution.IssueFromGitHub(item)
	}
	if !issue.HasLabel(contribution.ClaimedLabel) {
		if _, _, err := client.Issues.AddLabelsToIssue(ctx, owner, approved.Repository, approved.Number, []string{contribution.ClaimedLabel}); err != nil {
			return fmt.Errorf("add %q label to %s: %w", contribution.ClaimedLabel, *approved, err)
		}
	}
	if issue.HasLabel(contribution.HelpWantedLabel) {
		if _, err := client.Issues.RemoveLabelForIssue(ctx, owner, approved.Repository, approved.Number, contribution.HelpWantedLabel); err != nil &&
			!contribution.IsNotFound(err) {
			return fmt.Errorf("remove %q label from %s: %w", contribution.HelpWantedLabel, *approved, err)
		}
	}
	if !hasLabel(pullRequest, contribution.ClaimedLabel) {
		if _, _, err := client.Issues.AddLabelsToIssue(ctx, owner, repository, event.number, []string{contribution.ClaimedLabel}); err != nil {
			return fmt.Errorf("add %q label to pull request #%d: %w", contribution.ClaimedLabel, event.number, err)
		}
	}
	return nil
}

func createClaimRecord(ctx context.Context, client *github.Client, owner, repository string, pullRequestNumber int, reference contribution.IssueReference, author string) error {
	body := fmt.Sprintf("<!-- pr-gate:claim issue=%s author=%s -->", reference, author)
	_, _, err := client.Issues.CreateComment(ctx, owner, repository, pullRequestNumber, github.IssueCommentRequest{Body: body})
	if err != nil {
		return fmt.Errorf("create claim record for pull request #%d issue %s: %w", pullRequestNumber, reference, err)
	}
	return nil
}

func createIssueClaim(ctx context.Context, client *github.Client, owner string, reference contribution.IssueReference, pullRequestNumber int, author string) error {
	body := fmt.Sprintf("<!-- pr-gate:claim pr=%d author=%s -->\n\nIssue claimed by @%s in PR #%d.", pullRequestNumber, author, author, pullRequestNumber)
	_, _, err := client.Issues.CreateComment(ctx, owner, reference.Repository, reference.Number, github.IssueCommentRequest{Body: body})
	if err != nil {
		return fmt.Errorf("create issue claim for %s: %w", reference, err)
	}
	return nil
}

func ensureClaimedLabel(ctx context.Context, client *github.Client, owner, repository string) error {
	_, _, err := client.Issues.GetLabel(ctx, owner, repository, contribution.ClaimedLabel)
	if err == nil {
		return nil
	}
	if !contribution.IsNotFound(err) {
		return fmt.Errorf("read %q label in repository %s/%s: %w", contribution.ClaimedLabel, owner, repository, err)
	}
	color, description := "0e8a16", "A pull request has claimed this issue"
	_, _, err = client.Issues.CreateLabel(ctx, owner, repository, github.CreateIssueLabelRequest{
		Name: contribution.ClaimedLabel, Color: &color, Description: &description,
	})
	if err != nil {
		return fmt.Errorf("create %q label in repository %s/%s: %w", contribution.ClaimedLabel, owner, repository, err)
	}
	return nil
}

func hasLabel(item *contribution.PullRequest, name string) bool {
	if item == nil {
		return false
	}
	for _, label := range item.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}
