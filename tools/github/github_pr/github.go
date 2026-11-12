// Package github_pr implements GitHub pull request operations used by workflows.
package github_pr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/github_issue"
	"flock/tools/github/issue_reference"
)

const (
	GateMarker = "<!-- pr-gate:closed -->"
)

var (
	claimRecordMarker = regexp.MustCompile(`^<!-- pr-gate:claim issue=([A-Za-z0-9_.-]+)#([0-9]+) author=([A-Za-z0-9-]+) -->$`)
)

type Service struct {
	client *github.Client
}

type LinkedIssue struct {
	Owner           string
	Repository      string
	Number          int
	MilestoneNumber *int
}

type ClaimRecord struct {
	Issue     issue_reference.Reference
	Author    string
	CommentID int64
}

type claimRecordTransition uint8

const (
	createClaimRecord claimRecordTransition = iota
	reuseClaimRecord
)

// PullRequest is the GitHub pull request data used by workflows.
type PullRequest struct {
	Number            int
	State             string
	Merged            bool
	Body              string
	BaseRef           string
	Author            github_issue.User
	AuthorAssociation string
	Labels            []github_issue.Label
}

// File is a changed pull request file.
type File struct {
	Filename         string
	PreviousFilename string
}

// Event is a pull request webhook event.
type Event struct {
	Action     string
	Number     int
	Author     string
	BodyEdited bool
	BaseEdited bool
}

func New(client *github.Client) *Service {
	return &Service{client: client}
}

func (s *Service) Get(owner, repository string, number int) (*PullRequest, error) {
	pullRequest, _, err := s.client.PullRequests.Get(context.Background(), owner, repository, number)
	if err != nil {
		return nil, err
	}
	return pullRequestFromGitHub(pullRequest), nil
}

func (s *Service) GetMergeQueueEnqueuer(owner, repository string, number int) (string, error) {
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
	if _, err := s.client.Do(request, &response); err != nil {
		return "", err
	}
	if len(response.Errors) != 0 {
		return "", fmt.Errorf("get merge queue enqueuer for pull request #%d: %s", number, response.Errors[0].Message)
	}
	entry := response.Data.Repository.PullRequest.MergeQueueEntry
	if entry == nil || entry.Enqueuer.Login == "" {
		return "", fmt.Errorf("pull request #%d is not in the merge queue", number)
	}
	return entry.Enqueuer.Login, nil
}

func (s *Service) ListFiles(owner, repository string, number int) ([]File, error) {
	var files []File
	options := &github.ListOptions{PerPage: 100}
	for {
		items, response, err := s.client.PullRequests.ListFiles(context.Background(), owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			files = append(files, File{Filename: item.GetFilename(), PreviousFilename: item.GetPreviousFilename()})
		}
		if response.NextPage == 0 {
			return files, nil
		}
		options.Page = response.NextPage
	}
}

// ParseEvent parses a pull request webhook payload into owned workflow data.
func ParseEvent(data []byte) (Event, error) {
	var event github.PullRequestEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, err
	}
	changes := event.GetChanges()
	pullRequest := event.GetPullRequest()
	return Event{
		Action:     event.GetAction(),
		Number:     pullRequest.GetNumber(),
		Author:     pullRequest.GetUser().GetLogin(),
		BodyEdited: changes != nil && changes.Body != nil,
		BaseEdited: changes != nil && changes.Base != nil,
	}, nil
}

func pullRequestFromGitHub(source *github.PullRequest) *PullRequest {
	if source == nil {
		return nil
	}
	return &PullRequest{
		Number:            source.GetNumber(),
		State:             source.GetState(),
		Merged:            source.GetMerged(),
		Body:              source.GetBody(),
		BaseRef:           source.GetBase().GetRef(),
		Author:            github_issue.User{Login: source.GetUser().GetLogin(), Type: source.GetUser().GetType()},
		AuthorAssociation: source.GetAuthorAssociation(),
		Labels:            labelsFromGitHub(source.Labels),
	}
}

func labelsFromGitHub(sources []*github.Label) []github_issue.Label {
	labels := make([]github_issue.Label, 0, len(sources))
	for _, source := range sources {
		labels = append(labels, github_issue.Label{Name: source.GetName()})
	}
	return labels
}

func HasLabel(pullRequest *PullRequest, name string) bool {
	if pullRequest == nil {
		return false
	}
	for _, label := range pullRequest.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}

func (s *Service) ListLinkedIssues(owner, repository string, pullRequestNumber int) ([]LinkedIssue, error) {
	var cursor *string
	var issues []LinkedIssue
	for {
		request, err := s.client.NewRequest(context.Background(), "POST", "graphql", map[string]any{
			"query": `query($owner: String!, $repository: String!, $pullRequest: Int!, $cursor: String) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      closingIssuesReferences(first: 100, after: $cursor) {
        nodes { number milestone { number } repository { name owner { login } } }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`,
			"variables": map[string]any{
				"owner":       owner,
				"repository":  repository,
				"pullRequest": pullRequestNumber,
				"cursor":      cursor,
			},
		})
		if err != nil {
			return nil, err
		}

		var response struct {
			Errors []struct{ Message string }
			Data   struct {
				Repository struct {
					PullRequest struct {
						ClosingIssuesReferences struct {
							Nodes []struct {
								Number     int
								Milestone  *struct{ Number int }
								Repository struct {
									Name  string
									Owner struct{ Login string }
								}
							}
							PageInfo struct {
								HasNextPage bool
								EndCursor   string
							}
						}
					}
				}
			}
		}
		if _, err := s.client.Do(request, &response); err != nil {
			return nil, err
		}
		if len(response.Errors) != 0 {
			return nil, fmt.Errorf("list linked issues for pull request #%d: %s", pullRequestNumber, response.Errors[0].Message)
		}

		for _, issue := range response.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes {
			linkedIssue := LinkedIssue{Owner: issue.Repository.Owner.Login, Repository: issue.Repository.Name, Number: issue.Number}
			if issue.Milestone != nil {
				linkedIssue.MilestoneNumber = &issue.Milestone.Number
			}
			issues = append(issues, linkedIssue)
		}
		page := response.Data.Repository.PullRequest.ClosingIssuesReferences.PageInfo
		if !page.HasNextPage {
			return issues, nil
		}
		cursor = &page.EndCursor
	}
}

// FindClaimRecord returns the workflow-owned issue record on a pull request.
func (s *Service) FindClaimRecord(owner, repository string, pullRequestNumber int) (*ClaimRecord, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var record *ClaimRecord
	for {
		comments, response, err := s.client.Issues.ListComments(context.Background(), owner, repository, pullRequestNumber, options)
		if err != nil {
			return nil, err
		}
		for _, comment := range comments {
			if comment.GetUser().GetLogin() != "github-actions[bot]" {
				continue
			}
			current, ok := claimRecordFromComment(comment)
			if !ok || (record != nil && comment.GetID() <= record.CommentID) {
				continue
			}
			record = current
		}
		if response.NextPage == 0 {
			return record, nil
		}
		options.Page = response.NextPage
	}
}

func claimRecordFromComment(comment *github.IssueComment) (*ClaimRecord, bool) {
	if match := claimRecordMarker.FindStringSubmatch(comment.GetBody()); match != nil {
		number, err := strconv.Atoi(match[2])
		if err != nil || number == 0 {
			return nil, false
		}
		return &ClaimRecord{
			Issue:     issue_reference.Reference{Repository: match[1], Number: number},
			Author:    match[3],
			CommentID: comment.GetID(),
		}, true
	}
	return nil, false
}

// CreateClaimRecord writes one structured workflow-owned issue record unless a matching record already exists.
func (s *Service) CreateClaimRecord(owner, repository string, pullRequestNumber int, issue issue_reference.Reference, author string) (*ClaimRecord, error) {
	record, err := s.FindClaimRecord(owner, repository, pullRequestNumber)
	if err != nil {
		return nil, err
	}
	transition := claimRecordTransitionFor(record, issue, author)
	switch transition {
	case reuseClaimRecord:
		return record, nil
	case createClaimRecord:
		// Continue with the targeted comment write below.
	default:
		return nil, fmt.Errorf("unknown claim record transition %d", transition)
	}
	body := fmt.Sprintf("<!-- pr-gate:claim issue=%s author=%s -->", issue, author)
	comment, _, err := s.client.Issues.CreateComment(context.Background(), owner, repository, pullRequestNumber, github.IssueCommentRequest{Body: body})
	if err != nil {
		return nil, err
	}
	return &ClaimRecord{Issue: issue, Author: author, CommentID: comment.GetID()}, nil
}

func claimRecordTransitionFor(record *ClaimRecord, issue issue_reference.Reference, author string) claimRecordTransition {
	if record != nil && record.Issue == issue && record.Author == author {
		return reuseClaimRecord
	}
	return createClaimRecord
}

// DeleteClaimRecord deletes the exact workflow-owned record comment.
func (s *Service) DeleteClaimRecord(owner, repository string, record *ClaimRecord) error {
	if record == nil || record.CommentID == 0 {
		return nil
	}
	_, err := s.client.Issues.DeleteComment(context.Background(), owner, repository, record.CommentID)
	if isNotFound(err) {
		return nil
	}
	return err
}

func isNotFound(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == 404
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

func (s *Service) CreateComment(owner, repository string, number int, body string) error {
	_, _, err := s.client.Issues.CreateComment(context.Background(), owner, repository, number, github.IssueCommentRequest{Body: body})
	return err
}

func (s *Service) SetState(owner, repository string, number int, state string) error {
	_, _, err := s.client.PullRequests.Edit(context.Background(), owner, repository, number, &github.PullRequest{State: new(state)})
	return err
}

func (s *Service) TransferIssueMilestone(owner, repository string, pullRequestNumber, issueNumber int) (bool, error) {
	issue, _, err := s.client.Issues.Get(context.Background(), owner, repository, issueNumber)
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
