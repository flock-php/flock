package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
)

const (
	claimedLabel    = "claimed"
	helpWantedLabel = "help wanted"
	gateMarker      = "<!-- pr-gate:closed -->"
)

var claimRecordMarker = regexp.MustCompile(`^<!-- pr-gate:claim issue=([A-Za-z0-9_.-]+)#([0-9]+) author=([A-Za-z0-9-]+) -->$`)
var issueClaimMarker = regexp.MustCompile(`^<!-- pr-gate:claim pr=([0-9]+) author=([A-Za-z0-9-]+) -->(?:\r?\n|$)`)

type issueReference struct {
	Repository string
	Number     int
}

func (r issueReference) String() string { return fmt.Sprintf("%s#%d", r.Repository, r.Number) }

type user struct {
	Login string
	Type  string
}

type label struct{ Name string }

type issue struct {
	Number            int
	State             string
	IsPullRequest     bool
	Body              string
	User              user
	AuthorAssociation string
	Assignees         []user
	Labels            []label
}

type pullRequest struct {
	Number            int
	State             string
	Merged            bool
	Body              string
	BaseRef           string
	Author            user
	AuthorAssociation string
	Labels            []label
}

type file struct {
	Filename         string
	PreviousFilename string
}

type linkedIssue struct {
	Owner           string
	Repository      string
	Number          int
	MilestoneNumber *int
}

type claim struct {
	PullRequestNumber int
	Author            string
	CommentID         int64
}

type claimRecord struct {
	Issue     issueReference
	Author    string
	CommentID int64
}

type assignment struct{ Assigner string }

type pullRequestEvent struct {
	Action     string
	Number     int
	BodyEdited bool
	BaseEdited bool
}

type issueEvent struct {
	Action string
	Issue  issue
}

type mergeGroupEvent struct {
	HeadRef string
	HeadSHA string
}

type commandKind uint8

const (
	commandReadPullRequest commandKind = iota
	commandListLinkedIssues
	commandReadIssueMilestone
	commandSetPullRequestMilestone
	commandRemoveIssueMilestone
	commandWait
	commandDone
	commandListPullRequestFiles
	commandReadGateClosed
	commandReadClaimRecord
	commandCreateClaimRecord
	commandDeleteClaimRecord
	commandCreateComment
	commandSetPullRequestState
	commandReadIssue
	commandReadIssueClaim
	commandCreateIssueClaim
	commandDeleteIssueClaim
	commandAddIssueAssignee
	commandRemoveIssueAssignee
	commandAddIssueLabel
	commandRemoveIssueLabel
	commandEnsureClaimedLabel
	commandReadLatestAssignment
	commandRestoreHelpWanted
	commandReadRepositoryPermission
	commandReadRepositoryRole
	commandReadMergeQueueEnqueuer
	commandCancelOtherRuns
)

type command struct {
	kind              commandKind
	pullRequestNumber int
	issue             issueReference
	delay             time.Duration
	label             string
	username          string
	commentID         int64
	comment           string
	state             string
	milestoneNumber   *int
	headSHA           string
	runID             int64
}

type commandResult struct {
	pullRequest  *pullRequest
	files        []file
	linkedIssues []linkedIssue
	issue        *issue
	claim        *claim
	claimRecord  *claimRecord
	assignment   *assignment
	gateClosed   bool
	milestone    *int
	permission   string
	role         string
	enqueuer     string
	err          error
	failed       command
}

type githubAdapter struct{ client *github.Client }

func newGitHubAdapter(baseURL, token string) (*githubAdapter, error) {
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
	return &githubAdapter{client: client}, nil
}

func (a *githubAdapter) execute(owner, repository string, c command) (commandResult, error) {
	ctx := context.Background()
	switch c.kind {
	case commandReadPullRequest:
		pr, _, err := a.client.PullRequests.Get(ctx, owner, repository, c.pullRequestNumber)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{pullRequest: pullRequestFromGitHub(pr)}, nil
	case commandListPullRequestFiles:
		var files []file
		options := &github.ListOptions{PerPage: 100}
		for {
			items, response, err := a.client.PullRequests.ListFiles(ctx, owner, repository, c.pullRequestNumber, options)
			if err != nil {
				return commandResult{}, err
			}
			for _, item := range items {
				files = append(files, file{Filename: item.GetFilename(), PreviousFilename: item.GetPreviousFilename()})
			}
			if response.NextPage == 0 {
				return commandResult{files: files}, nil
			}
			options.Page = response.NextPage
		}
	case commandListLinkedIssues:
		return a.listLinkedIssues(ctx, owner, repository, c.pullRequestNumber)
	case commandReadIssueMilestone:
		item, _, err := a.client.Issues.Get(ctx, owner, c.issue.Repository, c.issue.Number)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		if err != nil {
			return commandResult{}, err
		}
		milestone := (*int)(nil)
		if item.Milestone != nil {
			value := item.Milestone.GetNumber()
			milestone = &value
		}
		return commandResult{issue: issueFromGitHub(item), milestone: milestone}, nil
	case commandSetPullRequestMilestone:
		_, _, err := a.client.Issues.Update(ctx, owner, repository, c.pullRequestNumber, github.UpdateIssueRequest{Milestone: c.milestoneNumber})
		return commandResult{}, err
	case commandRemoveIssueMilestone:
		_, _, err := a.client.Issues.RemoveMilestone(ctx, owner, c.issue.Repository, c.issue.Number)
		return commandResult{}, err
	case commandReadClaimRecord:
		record, err := a.findClaimRecord(ctx, owner, repository, c.pullRequestNumber)
		return commandResult{claimRecord: record}, err
	case commandCreateClaimRecord:
		body := fmt.Sprintf("<!-- pr-gate:claim issue=%s author=%s -->", c.issue, c.username)
		comment, _, err := a.client.Issues.CreateComment(ctx, owner, repository, c.pullRequestNumber, github.IssueCommentRequest{Body: body})
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{claimRecord: &claimRecord{Issue: c.issue, Author: c.username, CommentID: comment.GetID()}}, nil
	case commandDeleteClaimRecord:
		if c.commentID == 0 {
			return commandResult{}, nil
		}
		_, err := a.client.Issues.DeleteComment(ctx, owner, repository, c.commentID)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		return commandResult{}, err
	case commandCreateComment:
		_, _, err := a.client.Issues.CreateComment(ctx, owner, repository, c.pullRequestNumber, github.IssueCommentRequest{Body: c.comment})
		return commandResult{}, err
	case commandSetPullRequestState:
		_, _, err := a.client.PullRequests.Edit(ctx, owner, repository, c.pullRequestNumber, &github.PullRequest{State: new(c.state)})
		return commandResult{}, err
	case commandReadGateClosed:
		closed, err := a.gateClosed(ctx, owner, repository, c.pullRequestNumber)
		return commandResult{gateClosed: closed}, err
	case commandReadIssue:
		item, _, err := a.client.Issues.Get(ctx, owner, c.issue.Repository, c.issue.Number)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{issue: issueFromGitHub(item)}, nil
	case commandReadIssueClaim:
		item, err := a.findIssueClaim(ctx, owner, c.issue.Repository, c.issue.Number)
		return commandResult{claim: item}, err
	case commandCreateIssueClaim:
		body := fmt.Sprintf("<!-- pr-gate:claim pr=%d author=%s -->\n\nIssue claimed by @%s in PR #%d.", c.pullRequestNumber, c.username, c.username, c.pullRequestNumber)
		_, _, err := a.client.Issues.CreateComment(ctx, owner, c.issue.Repository, c.issue.Number, github.IssueCommentRequest{Body: body})
		return commandResult{}, err
	case commandDeleteIssueClaim:
		if c.commentID == 0 {
			return commandResult{}, nil
		}
		_, err := a.client.Issues.DeleteComment(ctx, owner, c.issue.Repository, c.commentID)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		return commandResult{}, err
	case commandAddIssueAssignee:
		item, _, err := a.client.Issues.AddAssignees(ctx, owner, c.issue.Repository, c.issue.Number, []string{c.username})
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{issue: issueFromGitHub(item)}, nil
	case commandRemoveIssueAssignee:
		_, _, err := a.client.Issues.RemoveAssignees(ctx, owner, c.issue.Repository, c.issue.Number, []string{c.username})
		if isNotFound(err) {
			return commandResult{}, nil
		}
		return commandResult{}, err
	case commandAddIssueLabel:
		labelOwner, labelRepository, labelNumber := owner, c.issue.Repository, c.issue.Number
		if c.pullRequestNumber != 0 && c.issue.Number == 0 {
			labelRepository, labelNumber = repository, c.pullRequestNumber
		}
		_, _, err := a.client.Issues.AddLabelsToIssue(ctx, labelOwner, labelRepository, labelNumber, []string{c.label})
		return commandResult{}, err
	case commandRemoveIssueLabel:
		_, err := a.client.Issues.RemoveLabelForIssue(ctx, owner, c.issue.Repository, c.issue.Number, c.label)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		return commandResult{}, err
	case commandEnsureClaimedLabel:
		_, _, err := a.client.Issues.GetLabel(ctx, owner, repository, claimedLabel)
		if err == nil {
			return commandResult{}, nil
		}
		if !isNotFound(err) {
			return commandResult{}, err
		}
		color, description := "0e8a16", "A pull request has claimed this issue"
		_, _, err = a.client.Issues.CreateLabel(ctx, owner, repository, github.CreateIssueLabelRequest{Name: claimedLabel, Color: &color, Description: &description})
		return commandResult{}, err
	case commandReadLatestAssignment:
		latest, err := a.latestAssignment(ctx, owner, c.issue.Repository, c.issue.Number, c.username)
		return commandResult{assignment: latest}, err
	case commandRestoreHelpWanted:
		item, _, err := a.client.Issues.Get(ctx, owner, c.issue.Repository, c.issue.Number)
		if isNotFound(err) {
			return commandResult{}, nil
		}
		if err != nil {
			return commandResult{}, err
		}
		current := issueFromGitHub(item)
		if current.State == "open" && len(current.Assignees) == 0 {
			_, _, err = a.client.Issues.AddLabelsToIssue(ctx, owner, c.issue.Repository, c.issue.Number, []string{helpWantedLabel})
		}
		return commandResult{}, err
	case commandReadRepositoryPermission:
		permission, _, err := a.client.Repositories.GetPermissionLevel(ctx, owner, repository, c.username)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{permission: permission.GetPermission()}, nil
	case commandReadRepositoryRole:
		permission, _, err := a.client.Repositories.GetPermissionLevel(ctx, owner, repository, c.username)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{role: permission.GetRoleName()}, nil
	case commandReadMergeQueueEnqueuer:
		request, err := a.client.NewRequest(ctx, "POST", "graphql", map[string]any{
			"query": `query($owner: String!, $repository: String!, $pullRequest: Int!) {
  repository(owner: $owner, name: $repository) {
    pullRequest(number: $pullRequest) {
      mergeQueueEntry { enqueuer { login } }
    }
  }
}`,
			"variables": map[string]any{"owner": owner, "repository": repository, "pullRequest": c.pullRequestNumber},
		})
		if err != nil {
			return commandResult{}, err
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
		if _, err := a.client.Do(request, &response); err != nil {
			return commandResult{}, err
		}
		if len(response.Errors) != 0 {
			return commandResult{}, fmt.Errorf("get merge queue enqueuer for pull request #%d: %s", c.pullRequestNumber, response.Errors[0].Message)
		}
		entry := response.Data.Repository.PullRequest.MergeQueueEntry
		if entry == nil || entry.Enqueuer.Login == "" {
			return commandResult{}, fmt.Errorf("pull request #%d is not in the merge queue", c.pullRequestNumber)
		}
		return commandResult{enqueuer: entry.Enqueuer.Login}, nil
	case commandCancelOtherRuns:
		runs, _, err := a.client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repository, &github.ListWorkflowRunsOptions{
			Event: "merge_group", HeadSHA: c.headSHA, PerPage: 100,
		})
		if err != nil {
			return commandResult{}, err
		}
		for _, run := range runs.WorkflowRuns {
			if run.GetID() == c.runID || run.GetStatus() == "completed" {
				continue
			}
			_, err := a.client.Actions.CancelWorkflowRunByID(ctx, owner, repository, run.GetID())
			if _, accepted := errors.AsType[*github.AcceptedError](err); err != nil && !accepted {
				return commandResult{}, fmt.Errorf("cancel run %d: %w", run.GetID(), err)
			}
		}
		return commandResult{}, nil
	case commandWait, commandDone:
		return commandResult{}, nil
	default:
		return commandResult{}, fmt.Errorf("unknown command kind %d", c.kind)
	}
}

func (a *githubAdapter) listLinkedIssues(ctx context.Context, owner, repository string, number int) (commandResult, error) {
	var cursor *string
	var issues []linkedIssue
	for {
		request, err := a.client.NewRequest(ctx, "POST", "graphql", map[string]any{
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
			"variables": map[string]any{"owner": owner, "repository": repository, "pullRequest": number, "cursor": cursor},
		})
		if err != nil {
			return commandResult{}, err
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
		if _, err := a.client.Do(request, &response); err != nil {
			return commandResult{}, err
		}
		if len(response.Errors) != 0 {
			return commandResult{}, fmt.Errorf("list linked issues for pull request #%d: %s", number, response.Errors[0].Message)
		}
		current := response.Data.Repository.PullRequest.ClosingIssuesReferences
		for _, item := range current.Nodes {
			linked := linkedIssue{Owner: item.Repository.Owner.Login, Repository: item.Repository.Name, Number: item.Number}
			if item.Milestone != nil {
				value := item.Milestone.Number
				linked.MilestoneNumber = &value
			}
			issues = append(issues, linked)
		}
		if !current.PageInfo.HasNextPage {
			return commandResult{linkedIssues: issues}, nil
		}
		cursor = &current.PageInfo.EndCursor
	}
}

func (a *githubAdapter) findClaimRecord(ctx context.Context, owner, repository string, number int) (*claimRecord, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var record *claimRecord
	for {
		comments, response, err := a.client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		for _, comment := range comments {
			if comment.GetUser().GetLogin() != "github-actions[bot]" {
				continue
			}
			match := claimRecordMarker.FindStringSubmatch(comment.GetBody())
			if match == nil {
				continue
			}
			issueNumber, err := strconv.Atoi(match[2])
			if err != nil || issueNumber == 0 || (record != nil && comment.GetID() <= record.CommentID) {
				continue
			}
			record = &claimRecord{Issue: issueReference{Repository: match[1], Number: issueNumber}, Author: match[3], CommentID: comment.GetID()}
		}
		if response.NextPage == 0 {
			return record, nil
		}
		options.Page = response.NextPage
	}
}

func (a *githubAdapter) findIssueClaim(ctx context.Context, owner, repository string, number int) (*claim, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var found *claim
	var foundID int64
	for {
		comments, response, err := a.client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		for _, comment := range comments {
			if comment.GetUser().GetLogin() != "github-actions[bot]" {
				continue
			}
			match := issueClaimMarker.FindStringSubmatch(comment.GetBody())
			if match == nil || (found != nil && comment.GetID() >= foundID) {
				continue
			}
			pullRequestNumber, err := strconv.Atoi(match[1])
			if err == nil && pullRequestNumber != 0 {
				found = &claim{PullRequestNumber: pullRequestNumber, Author: match[2], CommentID: comment.GetID()}
				foundID = comment.GetID()
			}
		}
		if response.NextPage == 0 {
			return found, nil
		}
		options.Page = response.NextPage
	}
}

func (a *githubAdapter) gateClosed(ctx context.Context, owner, repository string, number int) (bool, error) {
	var latestMarker time.Time
	comments, err := a.listAllComments(ctx, owner, repository, number)
	if err != nil {
		return false, err
	}
	for _, comment := range comments {
		if comment.GetUser().GetLogin() == "github-actions[bot]" && strings.Contains(comment.GetBody(), gateMarker) && comment.GetCreatedAt().After(latestMarker) {
			latestMarker = comment.GetCreatedAt().Time
		}
	}
	if latestMarker.IsZero() {
		return false, nil
	}
	events, err := a.listAllEvents(ctx, owner, repository, number)
	if err != nil {
		return false, err
	}
	var latestTransition *github.IssueEvent
	var latestReopen time.Time
	for _, event := range events {
		if event.GetEvent() == "reopened" && event.GetCreatedAt().After(latestReopen) {
			latestReopen = event.GetCreatedAt().Time
		}
		if (event.GetEvent() == "closed" || event.GetEvent() == "reopened") && (latestTransition == nil || !event.GetCreatedAt().Before(latestTransition.GetCreatedAt().Time)) {
			latestTransition = event
		}
	}
	return latestTransition != nil && latestTransition.GetEvent() == "closed" && latestTransition.GetActor().GetLogin() == "github-actions[bot]" && !latestMarker.Before(latestReopen) && !latestMarker.After(latestTransition.GetCreatedAt().Time), nil
}

func (a *githubAdapter) listAllComments(ctx context.Context, owner, repository string, number int) ([]*github.IssueComment, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var result []*github.IssueComment
	for {
		items, response, err := a.client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func (a *githubAdapter) listAllEvents(ctx context.Context, owner, repository string, number int) ([]*github.IssueEvent, error) {
	options := &github.ListOptions{PerPage: 100}
	var result []*github.IssueEvent
	for {
		items, response, err := a.client.Issues.ListIssueEvents(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func (a *githubAdapter) latestAssignment(ctx context.Context, owner, repository string, number int, username string) (*assignment, error) {
	options := &github.ListOptions{PerPage: 100}
	var latest time.Time
	var assigner string
	for {
		events, response, err := a.client.Issues.ListIssueEvents(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			if event.GetEvent() != "assigned" || event.GetAssignee().GetLogin() != username || !event.GetCreatedAt().After(latest) {
				continue
			}
			latest = event.GetCreatedAt().Time
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
	return &assignment{Assigner: assigner}, nil
}

func isNotFound(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

func pullRequestFromGitHub(source *github.PullRequest) *pullRequest {
	if source == nil {
		return nil
	}
	return &pullRequest{Number: source.GetNumber(), State: source.GetState(), Merged: source.GetMerged(), Body: source.GetBody(), BaseRef: source.GetBase().GetRef(), Author: user{Login: source.GetUser().GetLogin(), Type: source.GetUser().GetType()}, AuthorAssociation: source.GetAuthorAssociation(), Labels: labelsFromGitHub(source.Labels)}
}

func issueFromGitHub(source *github.Issue) *issue {
	if source == nil {
		return nil
	}
	return &issue{Number: source.GetNumber(), State: source.GetState(), IsPullRequest: source.IsPullRequest(), Body: source.GetBody(), User: user{Login: source.GetUser().GetLogin(), Type: source.GetUser().GetType()}, AuthorAssociation: source.GetAuthorAssociation(), Assignees: usersFromGitHub(source.Assignees), Labels: labelsFromGitHub(source.Labels)}
}

func usersFromGitHub(sources []*github.User) []user {
	result := make([]user, 0, len(sources))
	for _, source := range sources {
		result = append(result, user{Login: source.GetLogin(), Type: source.GetType()})
	}
	return result
}

func labelsFromGitHub(sources []*github.Label) []label {
	result := make([]label, 0, len(sources))
	for _, source := range sources {
		result = append(result, label{Name: source.GetName()})
	}
	return result
}

func hasLabel(item *pullRequest, name string) bool {
	if item == nil {
		return false
	}
	for _, current := range item.Labels {
		if current.Name == name {
			return true
		}
	}
	return false
}

func issueHasLabel(item *issue, name string) bool {
	if item == nil {
		return false
	}
	for _, current := range item.Labels {
		if current.Name == name {
			return true
		}
	}
	return false
}

func issueHasAssignee(item *issue, username string) bool {
	if item == nil {
		return false
	}
	for _, current := range item.Assignees {
		if current.Login == username {
			return true
		}
	}
	return false
}

type issueApprovalState uint8

const (
	issueApprovalDenied issueApprovalState = iota
	issueApprovalAssignedAuthor
	issueApprovalUnassignedHelpWanted
)

func issueApprovalFor(item *issue, author string) issueApprovalState {
	if item == nil || item.IsPullRequest || item.State != "open" {
		return issueApprovalDenied
	}
	if len(item.Assignees) != 0 {
		if issueHasAssignee(item, author) {
			return issueApprovalAssignedAuthor
		}
		return issueApprovalDenied
	}
	if issueHasLabel(item, helpWantedLabel) {
		return issueApprovalUnassignedHelpWanted
	}
	return issueApprovalDenied
}

type claimOwnershipState uint8

const (
	claimOwnershipNone claimOwnershipState = iota
	claimOwnershipThisPullRequest
	claimOwnershipAnotherPullRequest
	claimOwnershipSuperseded
)

func claimOwnershipFor(item *issue, current *claim, author string, pullRequestNumber int) claimOwnershipState {
	if current == nil {
		return claimOwnershipNone
	}
	for _, assignee := range item.Assignees {
		if assignee.Login != current.Author {
			return claimOwnershipSuperseded
		}
	}
	if current.Author == author && current.PullRequestNumber == pullRequestNumber {
		return claimOwnershipThisPullRequest
	}
	return claimOwnershipAnotherPullRequest
}

type authorizationState uint8

const (
	authorizationDenied authorizationState = iota
	authorizationMaintainerAssignment
	authorizationAutomaticClaimOwner
	authorizationCreateAutomaticClaim
	authorizationAutomaticClaimConflict
)

func authorizationFor(issueState issueApprovalState, claimState claimOwnershipState) authorizationState {
	switch issueState {
	case issueApprovalAssignedAuthor:
		switch claimState {
		case claimOwnershipNone, claimOwnershipSuperseded:
			return authorizationMaintainerAssignment
		case claimOwnershipThisPullRequest:
			return authorizationAutomaticClaimOwner
		case claimOwnershipAnotherPullRequest:
			return authorizationAutomaticClaimConflict
		}
	case issueApprovalUnassignedHelpWanted:
		switch claimState {
		case claimOwnershipNone:
			return authorizationCreateAutomaticClaim
		case claimOwnershipThisPullRequest:
			return authorizationAutomaticClaimOwner
		case claimOwnershipAnotherPullRequest:
			return authorizationAutomaticClaimConflict
		}
	}
	return authorizationDenied
}

func bypassesGate(author user, association string) bool {
	return author.Type == "Bot" || association == "OWNER" || association == "MEMBER" || association == "COLLABORATOR"
}

func docsOnly(files []file) bool {
	if len(files) == 0 {
		return false
	}
	for _, current := range files {
		if !strings.HasPrefix(current.Filename, "docs/") || (current.PreviousFilename != "" && !strings.HasPrefix(current.PreviousFilename, "docs/")) {
			return false
		}
	}
	return true
}

func parsePullRequestEvent(data []byte) (pullRequestEvent, error) {
	var event github.PullRequestEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return pullRequestEvent{}, err
	}
	changes := event.GetChanges()
	pr := event.GetPullRequest()
	return pullRequestEvent{Action: event.GetAction(), Number: pr.GetNumber(), BodyEdited: changes != nil && changes.Body != nil, BaseEdited: changes != nil && changes.Base != nil}, nil
}

func parseIssueEvent(data []byte) (issueEvent, error) {
	var event github.IssuesEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return issueEvent{}, err
	}
	item := issueFromGitHub(event.GetIssue())
	if item == nil {
		return issueEvent{Action: event.GetAction()}, nil
	}
	return issueEvent{Action: event.GetAction(), Issue: *item}, nil
}

func parseMergeGroupEvent(data []byte) (mergeGroupEvent, error) {
	var event github.MergeGroupEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return mergeGroupEvent{}, err
	}
	mergeGroup := event.GetMergeGroup()
	return mergeGroupEvent{HeadRef: mergeGroup.GetHeadRef(), HeadSHA: mergeGroup.GetHeadSHA()}, nil
}

func parseRepository(value string) (string, string, error) {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY must be owner/repository, got %q", value)
	}
	return owner, name, nil
}

var platformLabels = map[string]string{
	"macOS":   "os: mac",
	"Windows": "os: windows",
	"Linux":   "os: linux",
}

func issuePlatforms(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != "### Platform" {
			continue
		}
		for _, answer := range lines[index+1:] {
			answer = strings.TrimSpace(answer)
			if answer == "" {
				continue
			}
			var labels []string
			for _, platform := range strings.Split(answer, ",") {
				if label, ok := platformLabels[strings.TrimSpace(platform)]; ok {
					labels = append(labels, label)
				}
			}
			return labels
		}
	}
	return nil
}
