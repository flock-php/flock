// Package github_issue implements GitHub issue operations used by workflows.
package github_issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/google/go-github/v93/github"
)

const (
	Claimed    = "claimed"
	HelpWanted = "help wanted"
)

var claimMarker = regexp.MustCompile(`^<!-- pr-gate:claim pr=(\d+) author=([A-Za-z0-9-]+) -->(?:\r?\n|$)`)

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

// Claim identifies the pull request that owns an automatically claimed issue.
type Claim struct {
	PullRequestNumber int
	Author            string
	CommentID         int64
}

type Assignment struct {
	Assigner string
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

// CreateClaim records an automated claim before assigning the issue author.
func (s *Service) CreateClaim(owner, repository string, number, pullRequestNumber int, author string) error {
	body := fmt.Sprintf("<!-- pr-gate:claim pr=%d author=%s -->\n\nIssue claimed by @%s in PR #%d.", pullRequestNumber, author, author, pullRequestNumber)
	_, _, err := s.client.Issues.CreateComment(context.Background(), owner, repository, number, github.IssueCommentRequest{Body: body})
	return err
}

// FindClaim returns the first claim record written by the contribution workflow.
func (s *Service) FindClaim(owner, repository string, number int) (*Claim, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var claim *Claim
	var claimCommentID int64
	for {
		comments, response, err := s.client.Issues.ListComments(context.Background(), owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		for _, comment := range comments {
			current, ok := claimFromComment(comment)
			if !ok || (claim != nil && comment.GetID() >= claimCommentID) {
				continue
			}
			claim = current
			claimCommentID = comment.GetID()
		}
		if response.NextPage == 0 {
			return claim, nil
		}
		options.Page = response.NextPage
	}
}

func claimFromComment(comment *github.IssueComment) (*Claim, bool) {
	if comment.GetUser().GetLogin() != "github-actions[bot]" {
		return nil, false
	}
	match := claimMarker.FindStringSubmatch(comment.GetBody())
	if match == nil {
		return nil, false
	}
	pullRequestNumber, err := strconv.Atoi(match[1])
	if err != nil || pullRequestNumber == 0 {
		return nil, false
	}
	return &Claim{
		PullRequestNumber: pullRequestNumber,
		Author:            match[2],
		CommentID:         comment.GetID(),
	}, true
}

func (s *Service) DeleteClaim(owner, repository string, commentID int64) error {
	if commentID == 0 {
		return nil
	}
	_, err := s.client.Issues.DeleteComment(context.Background(), owner, repository, commentID)
	if isNotFound(err) {
		return nil
	}
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
	return s.removeLabelIfPresent(owner, repository, issue, HelpWanted)
}

// RemoveClaimed removes the durable claim display label when it is present.
func (s *Service) RemoveClaimed(owner, repository string, issue *Issue) error {
	return s.removeLabelIfPresent(owner, repository, issue, Claimed)
}

func (s *Service) removeLabelIfPresent(owner, repository string, issue *Issue, label string) error {
	if !HasLabel(issue, label) {
		return nil
	}
	err := s.RemoveLabel(owner, repository, issue.Number, label)
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
	return issueFromGitHub(issue), nil
}

func (s *Service) RemoveAssignee(owner, repository string, number int, username string) error {
	_, _, err := s.client.Issues.RemoveAssignees(context.Background(), owner, repository, number, []string{username})
	if isNotFound(err) {
		return nil
	}
	return err
}

func (s *Service) LatestAssignment(owner, repository string, number int, username string) (*Assignment, error) {
	var latest time.Time
	var assigner string
	options := &github.ListOptions{PerPage: 100}
	for {
		events, response, err := s.client.Issues.ListIssueEvents(context.Background(), owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			if event.GetEvent() != "assigned" || event.GetAssignee().GetLogin() != username {
				continue
			}
			createdAt := event.GetCreatedAt().Time
			if !createdAt.After(latest) {
				continue
			}
			latest = createdAt
			assigner = event.GetAssigner().GetLogin()
		}
		if response.NextPage == 0 {
			break
		}
		options.Page = response.NextPage
	}
	if latest.IsZero() {
		return nil, nil
	}
	return &Assignment{Assigner: assigner}, nil
}

func (s *Service) RestoreHelpWanted(owner, repository string, issue *Issue) error {
	current, err := s.Get(owner, repository, issue.Number)
	if err != nil {
		return err
	}
	if current == nil || current.State != "open" || len(current.Assignees) != 0 {
		return nil
	}
	return s.AddLabel(owner, repository, issue.Number, HelpWanted)
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

type IssueApprovalState uint8

const (
	IssueApprovalDenied IssueApprovalState = iota
	IssueApprovalAssignedAuthor
	IssueApprovalUnassignedHelpWanted
)

// IssueApprovalFor classifies whether the current issue state can authorize the author.
func IssueApprovalFor(issue *Issue, author string) IssueApprovalState {
	if issue == nil || issue.IsPullRequest || issue.State != "open" {
		return IssueApprovalDenied
	}
	if len(issue.Assignees) != 0 {
		if HasAssignee(issue, author) {
			return IssueApprovalAssignedAuthor
		}
		return IssueApprovalDenied
	}
	if HasHelpWanted(issue) {
		return IssueApprovalUnassignedHelpWanted
	}
	return IssueApprovalDenied
}

type ClaimOwnershipState uint8

const (
	ClaimOwnershipNone ClaimOwnershipState = iota
	ClaimOwnershipThisPullRequest
	ClaimOwnershipAnotherPullRequest
	ClaimOwnershipSuperseded
)

// ClaimOwnershipFor classifies the persisted automatic claim against the current issue.
func ClaimOwnershipFor(issue *Issue, claim *Claim, author string, pullRequestNumber int) ClaimOwnershipState {
	if claim == nil {
		return ClaimOwnershipNone
	}
	if hasAssigneeOtherThan(issue, claim.Author) {
		return ClaimOwnershipSuperseded
	}
	if claim.Author == author && claim.PullRequestNumber == pullRequestNumber {
		return ClaimOwnershipThisPullRequest
	}
	return ClaimOwnershipAnotherPullRequest
}

func hasAssigneeOtherThan(issue *Issue, username string) bool {
	if issue == nil {
		return false
	}
	for _, assignee := range issue.Assignees {
		if assignee.Login != username {
			return true
		}
	}
	return false
}

type AuthorizationState uint8

const (
	AuthorizationDenied AuthorizationState = iota
	AuthorizationMaintainerAssignment
	AuthorizationAutomaticClaimOwner
	AuthorizationCreateAutomaticClaim
	AuthorizationAutomaticClaimConflict
)

// AuthorizationFor combines issue approval and claim ownership into the final authorization state.
func AuthorizationFor(issueState IssueApprovalState, claimState ClaimOwnershipState) (AuthorizationState, error) {
	if issueState > IssueApprovalUnassignedHelpWanted || claimState > ClaimOwnershipSuperseded {
		return AuthorizationDenied, fmt.Errorf("unknown authorization states: issue=%d claim=%d", issueState, claimState)
	}
	switch issueState {
	case IssueApprovalDenied:
		return AuthorizationDenied, nil
	case IssueApprovalAssignedAuthor:
		switch claimState {
		case ClaimOwnershipNone, ClaimOwnershipSuperseded:
			return AuthorizationMaintainerAssignment, nil
		case ClaimOwnershipThisPullRequest:
			return AuthorizationAutomaticClaimOwner, nil
		case ClaimOwnershipAnotherPullRequest:
			return AuthorizationAutomaticClaimConflict, nil
		}
	case IssueApprovalUnassignedHelpWanted:
		switch claimState {
		case ClaimOwnershipNone:
			return AuthorizationCreateAutomaticClaim, nil
		case ClaimOwnershipThisPullRequest:
			return AuthorizationAutomaticClaimOwner, nil
		case ClaimOwnershipAnotherPullRequest:
			return AuthorizationAutomaticClaimConflict, nil
		case ClaimOwnershipSuperseded:
			return AuthorizationDenied, fmt.Errorf("unsupported authorization states: issue=%d claim=%d", issueState, claimState)
		}
	}
	return AuthorizationDenied, fmt.Errorf("unknown authorization states: issue=%d claim=%d", issueState, claimState)
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
