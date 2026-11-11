// Package pr_gate applies Flock's pull request contribution policy.
package pr_gate

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"flock/tools/github/github_issue"
	"flock/tools/github/github_pr"
	"flock/tools/github/issue_reference"
	"github.com/google/go-github/v93/github"
)

type PullRequestGateService interface {
	Get(owner, repository string, number int) (*github_pr.PullRequest, error)
	LowestOpenReference(owner, repository string, issue issue_reference.Reference) (*github_pr.PullRequest, error)
	ListFiles(owner, repository string, number int) ([]github_pr.File, error)
	GateClosed(owner, repository string, number int) (bool, error)
	CreateComment(owner, repository string, number int, body string) error
	SetState(owner, repository string, number int, state string) error
}

type IssueReader interface {
	Get(owner, repository string, number int) (*github_issue.Issue, error)
	EnsureClaimedLabel(owner, repository string) error
	AddLabel(owner, repository string, number int, label string) error
}

type Dependencies struct {
	Issues       IssueReader
	PullRequests PullRequestGateService
}

type Event struct {
	Action     string
	Number     int
	BodyEdited bool
	BaseEdited bool
}

type Result struct {
	ApprovedIssue *issue_reference.Reference
	ClaimedBy     string
}

type action string

const (
	allow           action = "allow"
	closeWrongBase  action = "close-wrong-base"
	closeUnapproved action = "close-unapproved"
)

func Execute(dependencies Dependencies, owner, repository string, event Event) (Result, error) {
	gate := pullRequestGate{dependencies: dependencies, owner: owner, repository: repository, event: event}
	for attempt := 0; ; attempt++ {
		result, retry, err := gate.evaluate(attempt < 2)
		if err != nil || !retry {
			return result, err
		}
		time.Sleep(3 * time.Second)
	}
}

type pullRequestGate struct {
	dependencies Dependencies
	owner        string
	repository   string
	event        Event
}

func (g pullRequestGate) evaluate(retryAllowed bool) (Result, bool, error) {
	pr, err := g.dependencies.PullRequests.Get(g.owner, g.repository, g.event.Number)
	if err != nil || pr == nil || bypassesGate(pr.Author, pr.AuthorAssociation) ||
		(pr.State == "open" && approvedPullRequest(pr) && pr.BaseRef == "main") {
		return Result{}, false, err
	}
	if pr.State == "closed" {
		return g.evaluateClosed(pr)
	}
	return g.evaluateOpen(pr, retryAllowed)
}

func (g pullRequestGate) evaluateClosed(pr *github_pr.PullRequest) (Result, bool, error) {
	if !g.event.BodyEdited || pr.Merged || pr.BaseRef != "main" {
		return Result{}, false, nil
	}
	closedByGate, err := g.dependencies.PullRequests.GateClosed(g.owner, g.repository, pr.Number)
	if err != nil || !closedByGate {
		return Result{}, false, err
	}
	issue, approved, _, err := firstApprovedIssue(g.dependencies.Issues, g.dependencies.PullRequests, g.owner, g.repository, pr)
	if err != nil {
		return Result{}, false, err
	}
	if !approved {
		return Result{}, false, nil
	}
	if err := g.dependencies.PullRequests.SetState(g.owner, g.repository, pr.Number, "open"); err != nil {
		return Result{}, false, err
	}
	comment := fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", pr.Author.Login, issue)
	if err := g.dependencies.PullRequests.CreateComment(g.owner, g.repository, pr.Number, comment); err != nil {
		return Result{}, false, err
	}
	return approvedResult(issue), false, nil
}

func (g pullRequestGate) evaluateOpen(pr *github_pr.PullRequest, retryAllowed bool) (Result, bool, error) {
	if g.event.Action == "edited" && !g.event.BaseEdited {
		return Result{}, false, nil
	}
	if pr.BaseRef != "main" {
		return Result{}, false, closePullRequest(g.dependencies.PullRequests, g.owner, g.repository, pr, closeWrongBase)
	}
	files, err := g.dependencies.PullRequests.ListFiles(g.owner, g.repository, pr.Number)
	if err != nil {
		return Result{}, false, err
	}
	if len(files) != 0 && evaluate(files) == allow {
		return Result{}, false, nil
	}
	issue, approved, claimedBy, err := firstApprovedIssue(g.dependencies.Issues, g.dependencies.PullRequests, g.owner, g.repository, pr)
	if err != nil {
		return Result{}, false, err
	}
	if claimedBy != "" {
		return Result{ApprovedIssue: &issue, ClaimedBy: claimedBy}, false, nil
	}
	if approved {
		return approvedResult(issue), false, nil
	}
	if evaluate(files) == allow {
		return Result{}, false, nil
	}
	if g.event.Action == "opened" && retryAllowed {
		return Result{}, true, nil
	}
	return Result{}, false, closePullRequest(g.dependencies.PullRequests, g.owner, g.repository, pr, closeUnapproved)
}

func approvedResult(issue issue_reference.Reference) Result {
	return Result{ApprovedIssue: &issue}
}

// CloseClaimedIssue closes a pull request whose approved issue was claimed after the gate read it.
func CloseClaimedIssue(pullRequests PullRequestGateService, owner, repository string, number int, issue issue_reference.Reference, claimant string) error {
	pullRequest, err := pullRequests.Get(owner, repository, number)
	if err != nil || pullRequest == nil || pullRequest.State != "open" {
		return err
	}
	comment := fmt.Sprintf("Thanks for the PR, @%s! Issue %s has already been claimed by @%s, so this pull request is closed. Please check other issues labeled `help wanted`.\n\n%s", pullRequest.Author.Login, issue, claimant, github_pr.GateMarker)
	if err := pullRequests.CreateComment(owner, repository, number, comment); err != nil {
		return err
	}
	return pullRequests.SetState(owner, repository, number, "closed")
}

func evaluate(files []github_pr.File) action {
	if docsOnly(files) {
		return allow
	}
	return closeUnapproved
}

func firstApprovedIssue(issues IssueReader, pullRequests PullRequestGateService, owner, repository string, pr *github_pr.PullRequest) (issue_reference.Reference, bool, string, error) {
	var competingIssue issue_reference.Reference
	var competingClaimant string
	for _, reference := range issue_reference.Parse(pr.Body, "flock") {
		if reference.Repository != repository {
			continue
		}
		issue, err := issues.Get(owner, reference.Repository, reference.Number)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return issue_reference.Reference{}, false, "", err
		}
		if !issueApproved(issue, pr.Author.Login) {
			continue
		}
		if len(issue.Assignees) != 0 {
			return reference, true, "", nil
		}
		winner, err := pullRequests.LowestOpenReference(owner, repository, reference)
		if err != nil {
			return issue_reference.Reference{}, false, "", err
		}
		if winner == nil || pr.Number < winner.Number {
			winner = pr
		}
		if winner.Number != pr.Number {
			if competingClaimant == "" {
				competingIssue = reference
				competingClaimant = winner.Author.Login
			}
			continue
		}
		return reference, true, "", nil
	}
	if competingClaimant != "" {
		return competingIssue, false, competingClaimant, nil
	}
	return issue_reference.Reference{}, false, "", nil
}

func isNotFound(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

// CompleteClaim marks a pull request as durably approved after its completion work succeeds.
func CompleteClaim(issues IssueReader, owner, repository string, number int) error {
	if err := issues.EnsureClaimedLabel(owner, repository); err != nil {
		return err
	}
	return issues.AddLabel(owner, repository, number, github_issue.Claimed)
}

func closePullRequest(pullRequests PullRequestGateService, owner, repository string, pr *github_pr.PullRequest, decision action) error {
	var reason, nextStep string
	switch decision {
	case closeWrongBase:
		reason = fmt.Sprintf("Pull requests go into `main`, not `%s`, so this one is closed.", pr.BaseRef)
		nextStep = "Please open a new pull request against `main`."
	case closeUnapproved:
		reason = "Flock takes pull requests only for issues a maintainer has approved, so this one is closed."
		nextStep = "Edit this pull request's description to reference an approved issue, then save it to reopen automatically."
	default:
		return fmt.Errorf("cannot close a pull request for %q", decision)
	}
	comment := fmt.Sprintf("Thanks for the PR, @%s! %s %s See [CONTRIBUTING.md](https://github.com/%s/%s/blob/main/CONTRIBUTING.md).\n\n%s", pr.Author.Login, reason, nextStep, owner, repository, github_pr.GateMarker)
	if err := pullRequests.CreateComment(owner, repository, pr.Number, comment); err != nil {
		return err
	}
	return pullRequests.SetState(owner, repository, pr.Number, "closed")
}

func bypassesGate(user github_issue.User, authorAssociation string) bool {
	return user.Type == "Bot" || authorAssociation == "OWNER" || authorAssociation == "MEMBER" || authorAssociation == "COLLABORATOR"
}

func docsOnly(files []github_pr.File) bool {
	for _, file := range files {
		if !strings.HasPrefix(file.Filename, "docs/") || (file.PreviousFilename != "" && !strings.HasPrefix(file.PreviousFilename, "docs/")) {
			return false
		}
	}
	return true
}

func issueApproved(issue *github_issue.Issue, author string) bool {
	if issue == nil || issue.IsPullRequest || issue.State != "open" {
		return false
	}
	return github_issue.HasHelpWanted(issue) || github_issue.HasAssignee(issue, author)
}

func approvedPullRequest(pr *github_pr.PullRequest) bool {
	return github_pr.HasLabel(pr, github_issue.Claimed)
}
