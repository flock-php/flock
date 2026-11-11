// Package github_issue implements GitHub issue operations used by workflows.
package github_issue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/go-github/v93/github"
)

const (
	Claimed    = "claimed"
	HelpWanted = "help wanted"
)

// User is the GitHub user data used by issue workflows.
type User struct {
	Login string
	Type  string
}

// Label is an issue label.
type Label struct{ Name string }

// Issue is the GitHub issue data used by workflows.
type Issue struct {
	Number            int
	State             string
	IsPullRequest     bool
	Body              string
	User              User
	AuthorAssociation string
	Assignees         []User
	Labels            []Label
}

// Event is an issue webhook event.
type Event struct {
	Action string
	Issue  Issue
}

type Service struct {
	client *github.Client
}

func New(client *github.Client) *Service {
	return &Service{client: client}
}

func (s *Service) Get(owner, repository string, number int) (*Issue, error) {
	issue, _, err := s.client.Issues.Get(context.Background(), owner, repository, number)
	if err != nil {
		return nil, err
	}
	return issueFromGitHub(issue), nil
}

func (s *Service) AddLabel(owner, repository string, number int, label string) error {
	_, _, err := s.client.Issues.AddLabelsToIssue(context.Background(), owner, repository, number, []string{label})
	return err
}

// EnsureClaimedLabel creates the durable pull request approval label when it is missing.
func (s *Service) EnsureClaimedLabel(owner, repository string) error {
	return s.ensureLabel(owner, repository, Claimed, "A pull request has claimed this issue")
}

func (s *Service) ensureLabel(owner, repository, name, description string) error {
	_, _, err := s.client.Issues.GetLabel(context.Background(), owner, repository, name)
	if err == nil {
		return nil
	}
	if !isNotFound(err) {
		return err
	}
	color := "0e8a16"
	_, _, err = s.client.Issues.CreateLabel(context.Background(), owner, repository, github.CreateIssueLabelRequest{
		Name: name, Color: &color, Description: &description,
	})
	return err
}

func (s *Service) RemoveLabel(owner, repository string, number int, label string) error {
	_, err := s.client.Issues.RemoveLabelForIssue(context.Background(), owner, repository, number, label)
	return err
}

func (s *Service) RemoveHelpWanted(owner, repository string, issue *Issue) error {
	if !HasHelpWanted(issue) {
		return nil
	}
	err := s.RemoveLabel(owner, repository, issue.Number, HelpWanted)
	if isNotFound(err) {
		return nil
	}
	return err
}

func isNotFound(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

func (s *Service) AddAssignee(owner, repository string, number int, username string) (*Issue, error) {
	issue, _, err := s.client.Issues.AddAssignees(context.Background(), owner, repository, number, []string{username})
	if err != nil {
		return nil, err
	}
	result := issueFromGitHub(issue)
	return result, nil
}

func (s *Service) RemoveAssignee(owner, repository string, number int, username string) error {
	_, _, err := s.client.Issues.RemoveAssignees(context.Background(), owner, repository, number, []string{username})
	return err
}

func HasHelpWanted(issue *Issue) bool {
	return HasLabel(issue, HelpWanted)
}

func HasLabel(issue *Issue, name string) bool {
	if issue == nil {
		return false
	}
	for _, label := range issue.Labels {
		if label.Name == name {
			return true
		}
	}
	return false
}

func HasAssignee(issue *Issue, username string) bool {
	if issue == nil {
		return false
	}
	for _, assignee := range issue.Assignees {
		if assignee.Login == username {
			return true
		}
	}
	return false
}

// ParseEvent parses an issue webhook payload into owned workflow data.
func ParseEvent(data []byte) (Event, error) {
	var event github.IssuesEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, err
	}
	issue := issueFromGitHub(event.GetIssue())
	if issue == nil {
		return Event{Action: event.GetAction()}, nil
	}
	return Event{Action: event.GetAction(), Issue: *issue}, nil
}

func issueFromGitHub(source *github.Issue) *Issue {
	if source == nil {
		return nil
	}
	return &Issue{
		Number:            source.GetNumber(),
		State:             source.GetState(),
		IsPullRequest:     source.IsPullRequest(),
		Body:              source.GetBody(),
		User:              userFromGitHub(source.GetUser()),
		AuthorAssociation: source.GetAuthorAssociation(),
		Assignees:         usersFromGitHub(source.Assignees),
		Labels:            labelsFromGitHub(source.Labels),
	}
}

func userFromGitHub(source *github.User) User {
	if source == nil {
		return User{}
	}
	return User{Login: source.GetLogin(), Type: source.GetType()}
}

func usersFromGitHub(sources []*github.User) []User {
	users := make([]User, 0, len(sources))
	for _, source := range sources {
		users = append(users, userFromGitHub(source))
	}
	return users
}

func labelsFromGitHub(sources []*github.Label) []Label {
	labels := make([]Label, 0, len(sources))
	for _, source := range sources {
		labels = append(labels, Label{Name: source.GetName()})
	}
	return labels
}
