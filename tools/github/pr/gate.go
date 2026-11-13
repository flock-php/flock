package pr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

const gateRetryDelay = 3 * time.Second

func readGateClosed(ctx context.Context, client *github.Client, owner, repository string, number int) (bool, error) {
	comments, err := listAllComments(ctx, client, owner, repository, number)
	if err != nil {
		return false, fmt.Errorf("read gate comments for pull request #%d: %w", number, err)
	}
	var latestMarker time.Time
	for _, comment := range comments {
		if comment.GetUser().GetLogin() == "github-actions[bot]" && strings.Contains(comment.GetBody(), contribution.GateMarker) &&
			comment.GetCreatedAt().After(latestMarker) {
			latestMarker = comment.GetCreatedAt().Time
		}
	}
	if latestMarker.IsZero() {
		return false, nil
	}
	events, err := listAllEvents(ctx, client, owner, repository, number)
	if err != nil {
		return false, fmt.Errorf("read gate events for pull request #%d: %w", number, err)
	}
	var latestTransition *github.IssueEvent
	var latestReopen time.Time
	for _, event := range events {
		if event.GetEvent() == "reopened" && event.GetCreatedAt().After(latestReopen) {
			latestReopen = event.GetCreatedAt().Time
		}
		if (event.GetEvent() == "closed" || event.GetEvent() == "reopened") &&
			(latestTransition == nil || !event.GetCreatedAt().Before(latestTransition.GetCreatedAt().Time)) {
			latestTransition = event
		}
	}
	return latestTransition != nil &&
		latestTransition.GetEvent() == "closed" &&
		latestTransition.GetActor().GetLogin() == "github-actions[bot]" &&
		!latestMarker.Before(latestReopen) &&
		!latestMarker.After(latestTransition.GetCreatedAt().Time), nil
}

func Gate(ctx context.Context, client *github.Client, owner, repository string, eventData []byte) error {
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
	if bypassesGate(pullRequest) || pullRequest.Merged {
		return nil
	}
	reopen := false
	if pullRequest.State == "closed" {
		if event.action != "edited" || !event.bodyEdited || pullRequest.BaseRef != "main" {
			return nil
		}
		reopen, err = readGateClosed(ctx, client, owner, repository, event.number)
		if err != nil || !reopen {
			return err
		}
	} else {
		if pullRequest.BaseRef != "main" {
			return closePullRequest(ctx, client, owner, repository, pullRequest, nil)
		}
	}

	files, err := listAllFiles(ctx, client, owner, repository, event.number)
	if err != nil {
		return err
	}
	if docsOnly(files) {
		return nil
	}
	var approved *contribution.IssueReference
	var denied *closeReason
	for attempt := 0; ; attempt++ {
		linked, readErr := listClosingReferences(ctx, client, owner, repository, event.number)
		if readErr != nil {
			return readErr
		}
		approved, denied, readErr = firstApproved(ctx, client, owner, repository, pullRequest.Author.Login, event.number, linked)
		if readErr != nil {
			return readErr
		}
		if approved != nil || event.action != "opened" || attempt >= 2 || reopen {
			break
		}
		if err := wait(ctx, gateRetryDelay); err != nil {
			return err
		}
	}
	if approved != nil {
		if reopen {
			if err := setPullRequestState(ctx, client, owner, repository, event.number, "open"); err != nil {
				return err
			}
			return createComment(ctx, client, owner, repository, event.number,
				fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", pullRequest.Author.Login, approved))
		}
		return nil
	}
	if reopen {
		return nil
	}
	return closePullRequest(ctx, client, owner, repository, pullRequest, denied)
}

func bypassesGate(pullRequest *contribution.PullRequest) bool {
	if pullRequest == nil {
		return true
	}
	return pullRequest.Author.Type == "Bot" || pullRequest.AuthorAssociation == "OWNER" ||
		pullRequest.AuthorAssociation == "MEMBER" || pullRequest.AuthorAssociation == "COLLABORATOR"
}

func docsOnly(files []contribution.File) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if !hasDocsPrefix(file.Filename) || (file.PreviousFilename != "" && !hasDocsPrefix(file.PreviousFilename)) {
			return false
		}
	}
	return true
}

func hasDocsPrefix(filename string) bool {
	return len(filename) >= len("docs/") && filename[:len("docs/")] == "docs/"
}

func closePullRequest(ctx context.Context, client *github.Client, owner, repository string, pullRequest *contribution.PullRequest, denied *closeReason) error {
	if pullRequest.State == "closed" {
		return nil
	}
	var body string
	if pullRequest.BaseRef != "main" {
		body = fmt.Sprintf("Thanks for the PR, @%s! Pull requests go into `main`, not `%s`, so this one is closed. Please open a new pull request against `main`. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).",
			pullRequest.Author.Login, pullRequest.BaseRef, owner, repository)
	} else if denied != nil {
		body = fmt.Sprintf("Thanks for the PR, @%s! Issue %s is assigned to %s, so this pull request is closed. Edit this pull request's description to reference an approved issue, then save it to reopen automatically. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).",
			pullRequest.Author.Login, denied.Issue, denied.Names, owner, repository)
	} else {
		body = fmt.Sprintf("Thanks for the PR, @%s! Flock takes pull requests only for issues a maintainer has approved, so this one is closed. Edit this pull request's description to reference an approved issue, then save it to reopen automatically. See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).",
			pullRequest.Author.Login, owner, repository)
	}
	body += "\n\n" + contribution.GateMarker
	if err := createComment(ctx, client, owner, repository, pullRequest.Number, body); err != nil {
		return err
	}
	return setPullRequestState(ctx, client, owner, repository, pullRequest.Number, "closed")
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
