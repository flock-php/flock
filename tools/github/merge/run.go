package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/google/go-github/v93/github"
)

var mergeQueueRef = regexp.MustCompile(`/pr-(\d+)-[0-9a-f]+$`)

var ErrNotAllowed = errors.New("queued by someone without the admin or maintain role")

type mergeGroupEvent struct {
	HeadRef string
	HeadSHA string
}

func Run(ctx context.Context, client *github.Client, owner, repository string, data []byte, runIDValue string) error {
	event, err := parseMergeGroupEvent(data)
	if err != nil {
		return fmt.Errorf("parse merge group event: %w", err)
	}
	runID, _ := strconv.ParseInt(runIDValue, 10, 64)
	match := mergeQueueRef.FindStringSubmatch(event.HeadRef)
	if match == nil {
		return fmt.Errorf("not a merge queue ref: %q", event.HeadRef)
	}
	prNumber, err := strconv.Atoi(match[1])
	if err != nil {
		return fmt.Errorf("parse pull request number: %w", err)
	}

	enqueuer, err := mergeQueueEnqueuer(ctx, client, owner, repository, prNumber)
	if err != nil {
		return err
	}
	permission, _, err := client.Repositories.GetPermissionLevel(ctx, owner, repository, enqueuer)
	if err != nil {
		return fmt.Errorf("read repository role for @%s: %w", enqueuer, err)
	}
	role := permission.GetRoleName()
	if role == "admin" || role == "maintain" {
		return nil
	}
	if _, _, err := client.Issues.CreateComment(ctx, owner, repository, prNumber, github.IssueCommentRequest{
		Body: fmt.Sprintf("Only a repository admin or maintainer can merge, so this was taken out of the merge queue (queued by @%s). A maintainer will merge it.", enqueuer),
	}); err != nil {
		return fmt.Errorf("create merge queue comment on pull request #%d: %w", prNumber, err)
	}
	if err := cancelOtherRuns(ctx, client, owner, repository, event.HeadSHA, runID); err != nil {
		return err
	}
	return fmt.Errorf("%w: @%s has the %q role", ErrNotAllowed, enqueuer, role)
}

func parseMergeGroupEvent(data []byte) (mergeGroupEvent, error) {
	var event github.MergeGroupEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return mergeGroupEvent{}, err
	}
	mergeGroup := event.GetMergeGroup()
	return mergeGroupEvent{HeadRef: mergeGroup.GetHeadRef(), HeadSHA: mergeGroup.GetHeadSHA()}, nil
}

func mergeQueueEnqueuer(ctx context.Context, client *github.Client, owner, repository string, pullRequestNumber int) (string, error) {
	request, err := client.NewRequest(ctx, "POST", "graphql", map[string]any{
		"query": `query($owner: String!, $repository: String!, $pullRequest: Int!) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      mergeQueueEntry { enqueuer { login } }
    }
  }
}`,
		"variables": map[string]any{"owner": owner, "repository": repository, "pullRequest": pullRequestNumber},
	})
	if err != nil {
		return "", fmt.Errorf("build merge queue request for pull request #%d: %w", pullRequestNumber, err)
	}
	var response struct {
		Errors []struct{ Message string }
		Data   struct {
			Repository struct {
				PullRequest struct {
					MergeQueueEntry *struct {
						Enqueuer struct{ Login string }
					}
				}
			}
		}
	}
	if _, err := client.Do(request, &response); err != nil {
		return "", fmt.Errorf("read merge queue enqueuer for pull request #%d: %w", pullRequestNumber, err)
	}
	if len(response.Errors) != 0 {
		return "", fmt.Errorf("read merge queue enqueuer for pull request #%d: %s", pullRequestNumber, response.Errors[0].Message)
	}
	entry := response.Data.Repository.PullRequest.MergeQueueEntry
	if entry == nil || entry.Enqueuer.Login == "" {
		return "", fmt.Errorf("pull request #%d is not in the merge queue", pullRequestNumber)
	}
	return entry.Enqueuer.Login, nil
}

func cancelOtherRuns(ctx context.Context, client *github.Client, owner, repository, headSHA string, runID int64) error {
	runs, _, err := client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repository, &github.ListWorkflowRunsOptions{
		Event: "merge_group", HeadSHA: headSHA, PerPage: 100,
	})
	if err != nil {
		return fmt.Errorf("list merge-group workflow runs: %w", err)
	}
	for _, run := range runs.WorkflowRuns {
		if run.GetID() == runID || run.GetStatus() == "completed" {
			continue
		}
		if _, err := client.Actions.CancelWorkflowRunByID(ctx, owner, repository, run.GetID()); err != nil {
			if _, ok := errors.AsType[*github.AcceptedError](err); !ok {
				return fmt.Errorf("cancel run %d: %w", run.GetID(), err)
			}
		}
	}
	return nil
}
