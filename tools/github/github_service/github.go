// Package github_service implements the GitHub operations used by the workflows.
package github_service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
)

const GateMarker = "<!-- pr-gate:closed -->"

type Service struct {
	client *github.Client
}

type LinkedIssue struct {
	Number          int
	MilestoneNumber *int
}

func New(baseURL, token string) (*Service, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	client, err := github.NewClient(
		github.WithAuthToken(token),
		github.WithURLs(new(parsed.String()), nil),
	)
	if err != nil {
		return nil, err
	}
	return &Service{client: client}, nil
}

func (s *Service) PullRequest(owner, repository string, number int) (*github.PullRequest, error) {
	pullRequest, _, err := s.client.PullRequests.Get(context.Background(), owner, repository, number)
	return pullRequest, err
}

func (s *Service) Permission(owner, repository, username string) (string, error) {
	permission, _, err := s.client.Repositories.GetPermissionLevel(context.Background(), owner, repository, username)
	if err != nil {
		return "", err
	}
	return permission.GetPermission(), nil
}

// RoleName returns the user's effective role on the repository, including "maintain", which the
// legacy permission field reports as "write".
func (s *Service) RoleName(owner, repository, username string) (string, error) {
	permission, _, err := s.client.Repositories.GetPermissionLevel(context.Background(), owner, repository, username)
	if err != nil {
		return "", err
	}
	return permission.GetRoleName(), nil
}

// MergeQueueEnqueuer returns who added the pull request to the merge queue. With auto-merge,
// that's whoever enabled it.
func (s *Service) MergeQueueEnqueuer(owner, repository string, number int) (string, error) {
	request, err := s.client.NewRequest(context.Background(), "POST", "graphql", map[string]any{
		"query": `query($owner: String!, $repository: String!, $pullRequest: Int!) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      mergeQueueEntry { enqueuer { login } }
    }
  }
}`,
		"variables": map[string]any{
			"owner":       owner,
			"repository":  repository,
			"pullRequest": number,
		},
	})
	if err != nil {
		return "", err
	}
	var response struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					MergeQueueEntry *struct {
						Enqueuer struct{ Login string }
					}
				}
			}
		}
	}
	if _, err := s.client.Do(request, &response); err != nil {
		return "", err
	}
	entry := response.Data.Repository.PullRequest.MergeQueueEntry
	if entry == nil || entry.Enqueuer.Login == "" {
		return "", fmt.Errorf("pull request #%d is not in the merge queue", number)
	}
	return entry.Enqueuer.Login, nil
}

// CancelMergeGroupRuns cancels the other unfinished runs for a merge group commit.
func (s *Service) CancelMergeGroupRuns(owner, repository, headSHA string, except int64) error {
	runs, _, err := s.client.Actions.ListRepositoryWorkflowRuns(context.Background(), owner, repository, &github.ListWorkflowRunsOptions{
		Event:       "merge_group",
		HeadSHA:     headSHA,
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		return err
	}
	for _, run := range runs.WorkflowRuns {
		if run.GetID() == except || run.GetStatus() == "completed" {
			continue
		}
		_, err := s.client.Actions.CancelWorkflowRunByID(context.Background(), owner, repository, run.GetID())
		if _, accepted := errors.AsType[*github.AcceptedError](err); err != nil && !accepted {
			return fmt.Errorf("cancel run %d: %w", run.GetID(), err)
		}
	}
	return nil
}

func (s *Service) Files(owner, repository string, number int) ([]*github.CommitFile, error) {
	var files []*github.CommitFile
	options := &github.ListOptions{PerPage: 100}
	for {
		items, response, err := s.client.PullRequests.ListFiles(context.Background(), owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		files = append(files, items...)
		if response.NextPage == 0 {
			return files, nil
		}
		options.Page = response.NextPage
	}
}

func (s *Service) Issue(owner, repository string, number int) (*github.Issue, error) {
	issue, _, err := s.client.Issues.Get(context.Background(), owner, repository, number)
	return issue, err
}

func (s *Service) LinkedIssues(owner, repository string, pullRequestNumber int) ([]LinkedIssue, error) {
	request, err := s.client.NewRequest(context.Background(), "POST", "graphql", map[string]any{
		"query": `query($owner: String!, $repository: String!, $pullRequest: Int!) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      closingIssuesReferences(first: 100) {
        nodes { number milestone { number } }
      }
    }
  }
}`,
		"variables": map[string]any{
			"owner":       owner,
			"repository":  repository,
			"pullRequest": pullRequestNumber,
		},
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ClosingIssuesReferences struct {
						Nodes []struct {
							Number    int
							Milestone *struct{ Number int }
						}
					}
				}
			}
		}
	}
	if _, err := s.client.Do(request, &response); err != nil {
		return nil, err
	}
	issues := make([]LinkedIssue, 0, len(response.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes))
	for _, issue := range response.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes {
		linkedIssue := LinkedIssue{Number: issue.Number}
		if issue.Milestone != nil {
			linkedIssue.MilestoneNumber = &issue.Milestone.Number
		}
		issues = append(issues, linkedIssue)
	}
	return issues, nil
}

func (s *Service) GateClosed(owner, repository string, number int) (bool, error) {
	var latestMarker time.Time
	commentOptions := &github.IssueListCommentsOptions{PerPage: 100}
	for {
		comments, response, err := s.client.Issues.ListComments(context.Background(), owner, repository, number, commentOptions)
		if err != nil {
			return false, err
		}
		for _, comment := range comments {
			if comment.GetUser().GetLogin() == "github-actions[bot]" && strings.Contains(comment.GetBody(), GateMarker) && comment.GetCreatedAt().After(latestMarker) {
				latestMarker = comment.GetCreatedAt().Time
			}
		}
		if response.NextPage == 0 {
			break
		}
		commentOptions.Page = response.NextPage
	}
	if latestMarker.IsZero() {
		return false, nil
	}

	var latestTransition *github.IssueEvent
	var latestReopen time.Time
	eventOptions := &github.ListOptions{PerPage: 100}
	for {
		events, response, err := s.client.Issues.ListIssueEvents(context.Background(), owner, repository, number, eventOptions)
		if err != nil {
			return false, err
		}
		for _, current := range events {
			if current.GetEvent() == "reopened" && current.GetCreatedAt().After(latestReopen) {
				latestReopen = current.GetCreatedAt().Time
			}
			if (current.GetEvent() == "closed" || current.GetEvent() == "reopened") &&
				(latestTransition == nil || !current.GetCreatedAt().Before(latestTransition.GetCreatedAt().Time)) {
				latestTransition = current
			}
		}
		if response.NextPage == 0 {
			break
		}
		eventOptions.Page = response.NextPage
	}

	return latestTransition != nil &&
		latestTransition.GetEvent() == "closed" &&
		latestTransition.GetActor().GetLogin() == "github-actions[bot]" &&
		!latestMarker.Before(latestReopen) &&
		!latestMarker.After(latestTransition.GetCreatedAt().Time), nil
}

func (s *Service) Comment(owner, repository string, number int, body string) error {
	_, _, err := s.client.Issues.CreateComment(context.Background(), owner, repository, number, github.IssueCommentRequest{Body: body})
	return err
}

func (s *Service) SetPullRequestState(owner, repository string, number int, state string) error {
	_, _, err := s.client.PullRequests.Edit(context.Background(), owner, repository, number, &github.PullRequest{State: new(state)})
	return err
}

func (s *Service) AddLabel(owner, repository string, number int, label string) error {
	_, _, err := s.client.Issues.AddLabelsToIssue(context.Background(), owner, repository, number, []string{label})
	return err
}

func (s *Service) TransferMilestone(owner, repository string, pullRequestNumber, issueNumber int) (bool, error) {
	issue, err := s.Issue(owner, repository, issueNumber)
	if err != nil {
		return false, err
	}
	if issue.IsPullRequest() || issue.Milestone == nil {
		return false, nil
	}
	milestone := issue.Milestone.GetNumber()
	if _, _, err := s.client.Issues.Update(context.Background(), owner, repository, pullRequestNumber, github.UpdateIssueRequest{Milestone: &milestone}); err != nil {
		return false, fmt.Errorf("set pull request milestone: %w", err)
	}
	if _, _, err := s.client.Issues.RemoveMilestone(context.Background(), owner, repository, issueNumber); err != nil {
		return false, fmt.Errorf("remove issue milestone: %w", err)
	}
	return true, nil
}
