// Package pr_gate applies Flock's pull request contribution policy.
package pr_gate

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/github_issue"
	"flock/tools/github/github_pr"
	"flock/tools/github/issue_reference"
)

type PullRequestGateService interface {
	Get(owner, repository string, number int) (*github_pr.PullRequest, error)
	ListFiles(owner, repository string, number int) ([]github_pr.File, error)
	ListLinkedIssues(owner, repository string, pullRequestNumber int) ([]github_pr.LinkedIssue, error)
	GateClosed(owner, repository string, number int) (bool, error)
	ClaimCommentExists(owner, repository string, number int) (bool, error)
	CreateComment(owner, repository string, number int, body string) error
	SetState(owner, repository string, number int, state string) error
}

type IssueReader interface {
	Get(owner, repository string, number int) (*github_issue.Issue, error)
	FindClaim(owner, repository string, number int) (*github_issue.Claim, error)
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

type gateTransitionState uint8

const (
	noAction gateTransitionState = iota
	reopen
	approve
	conflict
	retryApproval
	closeWrongBase
	closeUnapproved
)

type gateTransition struct {
	state       gateTransitionState
	pullRequest *github_pr.PullRequest
	issue       issue_reference.Reference
	claimant    string
}

func Execute(dependencies Dependencies, owner, repository string, event Event) (Result, error) {
	gate := pullRequestGate{
		dependencies: dependencies,
		owner:        owner,
		repository:   repository,
		event:        event,
	}
	for attempt := 0; ; attempt++ {
		transition, err := gate.evaluate(attempt < 2)
		if err != nil {
			return Result{}, err
		}
		result, retry, err := gate.apply(transition)
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

func (g pullRequestGate) evaluate(retryAllowed bool) (gateTransition, error) {
	pr, err := g.dependencies.PullRequests.Get(g.owner, g.repository, g.event.Number)
	if err != nil || pr == nil {
		return gateTransition{}, err
	}
	if bypassesGate(pr.Author, pr.AuthorAssociation) || (pr.State == "open" && approvedPullRequest(pr) && pr.BaseRef == "main") {
		return gateTransition{
			state:       noAction,
			pullRequest: pr,
		}, nil
	}
	if pr.State == "closed" {
		return g.evaluateClosed(pr)
	}
	return g.evaluateOpen(pr, retryAllowed)
}

func (g pullRequestGate) evaluateClosed(pr *github_pr.PullRequest) (gateTransition, error) {
	if !g.event.BodyEdited || pr.Merged || pr.BaseRef != "main" {
		return gateTransition{
			state:       noAction,
			pullRequest: pr,
		}, nil
	}
	closedByGate, err := g.dependencies.PullRequests.GateClosed(g.owner, g.repository, pr.Number)
	if err != nil {
		return gateTransition{}, err
	}
	if !closedByGate {
		return gateTransition{
			state:       noAction,
			pullRequest: pr,
		}, nil
	}
	decision, err := firstApprovedIssue(g.dependencies.Issues, g.dependencies.PullRequests, g.owner, g.repository, pr)
	if err != nil {
		return gateTransition{}, err
	}
	if decision.state != approvedLinkedIssue {
		return gateTransition{
			state:       noAction,
			pullRequest: pr,
		}, nil
	}
	return gateTransition{
		state:       reopen,
		pullRequest: pr,
		issue:       decision.issue,
	}, nil
}

func (g pullRequestGate) evaluateOpen(pr *github_pr.PullRequest, retryAllowed bool) (gateTransition, error) {
	if g.event.Action == "edited" && !g.event.BaseEdited {
		return gateTransition{
			state:       noAction,
			pullRequest: pr,
		}, nil
	}
	if pr.BaseRef != "main" {
		return gateTransition{
			state:       closeWrongBase,
			pullRequest: pr,
		}, nil
	}
	files, err := g.dependencies.PullRequests.ListFiles(g.owner, g.repository, pr.Number)
	if err != nil {
		return gateTransition{}, err
	}
	if docsOnly(files) == noAction {
		return gateTransition{
			state:       noAction,
			pullRequest: pr,
		}, nil
	}
	decision, err := firstApprovedIssue(g.dependencies.Issues, g.dependencies.PullRequests, g.owner, g.repository, pr)
	if err != nil {
		return gateTransition{}, err
	}
	switch decision.state {
	case approvedLinkedIssue:
		return gateTransition{
			state:       approve,
			pullRequest: pr,
			issue:       decision.issue,
		}, nil
	case conflictingLinkedIssueClaim:
		return gateTransition{
			state:       conflict,
			pullRequest: pr,
			issue:       decision.issue,
			claimant:    decision.claimant,
		}, nil
	default:
		if retryAllowed && g.event.Action == "opened" {
			return gateTransition{
				state:       retryApproval,
				pullRequest: pr,
			}, nil
		}
		return gateTransition{
			state:       closeUnapproved,
			pullRequest: pr,
		}, nil
	}
}

func (g pullRequestGate) apply(transition gateTransition) (Result, bool, error) {
	switch transition.state {
	case noAction:
		return Result{}, false, nil
	case reopen:
		if err := g.dependencies.PullRequests.SetState(g.owner, g.repository, transition.pullRequest.Number, "open"); err != nil {
			return Result{}, false, err
		}
		comment := fmt.Sprintf("Thanks, @%s! This pull request references approved issue %s, so it has been reopened.", transition.pullRequest.Author.Login, transition.issue)
		if err := g.dependencies.PullRequests.CreateComment(g.owner, g.repository, transition.pullRequest.Number, comment); err != nil {
			return Result{}, false, err
		}
		return approvedResult(transition.issue), false, nil
	case approve:
		return approvedResult(transition.issue), false, nil
	case conflict:
		return Result{
			ApprovedIssue: &transition.issue,
			ClaimedBy:     transition.claimant,
		}, false, nil
	case retryApproval:
		return Result{}, true, nil
	case closeWrongBase, closeUnapproved:
		return Result{}, false, closePullRequest(g.dependencies.PullRequests, g.owner, g.repository, transition.pullRequest, transition.state)
	default:
		return Result{}, false, fmt.Errorf("unknown pull request gate transition %d", transition.state)
	}
}

func approvedResult(issue issue_reference.Reference) Result {
	return Result{
		ApprovedIssue: &issue,
	}
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

// EnsureClaimComment tells contributors when their pull request owns an automated issue claim.
func EnsureClaimComment(pullRequests PullRequestGateService, owner, repository string, number int, issue issue_reference.Reference, claimant string) error {
	exists, err := pullRequests.ClaimCommentExists(owner, repository, number)
	if err != nil || exists {
		return err
	}
	comment := fmt.Sprintf("Issue %s is claimed by @%s in PR #%d.\n\n%s", issue, claimant, number, github_pr.ClaimMarker)
	return pullRequests.CreateComment(owner, repository, number, comment)
}

func firstApprovedIssue(issues IssueReader, pullRequests PullRequestGateService, owner, repository string, pr *github_pr.PullRequest) (linkedIssueDecision, error) {
	linkedIssues, err := pullRequests.ListLinkedIssues(owner, repository, pr.Number)
	if err != nil {
		return linkedIssueDecision{}, err
	}
	var conflict linkedIssueDecision
	for _, linkedIssue := range linkedIssues {
		if linkedIssue.Owner != owner || linkedIssue.Repository != repository {
			continue
		}
		reference := issue_reference.Reference{Repository: linkedIssue.Repository, Number: linkedIssue.Number}
		issue, err := issues.Get(owner, reference.Repository, reference.Number)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return linkedIssueDecision{}, err
		}
		switch github_issue.ApprovalFor(issue, pr.Author.Login) {
		case github_issue.ApprovalDenied:
			continue
		case github_issue.ApprovalByAssignment:
			return linkedIssueDecision{
				state: approvedLinkedIssue,
				issue: reference,
			}, nil
		case github_issue.ApprovalRequiresAutomaticClaim:
			claim, err := issues.FindClaim(owner, reference.Repository, reference.Number)
			if err != nil {
				return linkedIssueDecision{}, err
			}
			if claim != nil {
				if claim.PullRequestNumber == pr.Number && claim.Author == pr.Author.Login {
					return linkedIssueDecision{
						state: approvedLinkedIssue,
						issue: reference,
					}, nil
				}
				if conflict.state == noApprovedLinkedIssue {
					conflict = linkedIssueDecision{
						state:    conflictingLinkedIssueClaim,
						issue:    reference,
						claimant: claim.Author,
					}
				}
				continue
			}
			return linkedIssueDecision{
				state: approvedLinkedIssue,
				issue: reference,
			}, nil
		}
	}
	if conflict.state == conflictingLinkedIssueClaim {
		return conflict, nil
	}
	return linkedIssueDecision{}, nil
}

type linkedIssueState uint8

const (
	noApprovedLinkedIssue linkedIssueState = iota
	approvedLinkedIssue
	conflictingLinkedIssueClaim
)

type linkedIssueDecision struct {
	state    linkedIssueState
	issue    issue_reference.Reference
	claimant string
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

func closePullRequest(pullRequests PullRequestGateService, owner, repository string, pr *github_pr.PullRequest, decision gateTransitionState) error {
	var reason, nextStep string
	switch decision {
	case closeWrongBase:
		reason = fmt.Sprintf("Pull requests go into `main`, not `%s`, so this one is closed.", pr.BaseRef)
		nextStep = "Please open a new pull request against `main`."
	case closeUnapproved:
		reason = "Flock takes pull requests only for issues a maintainer has approved, so this one is closed."
		nextStep = "Edit this pull request's description to reference an approved issue, then save it to reopen automatically."
	default:
		return fmt.Errorf("cannot close a pull request for transition %d", decision)
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

func docsOnly(files []github_pr.File) gateTransitionState {
	if len(files) == 0 {
		return closeUnapproved
	}
	for _, file := range files {
		if !strings.HasPrefix(file.Filename, "docs/") || (file.PreviousFilename != "" && !strings.HasPrefix(file.PreviousFilename, "docs/")) {
			return closeUnapproved
		}
	}
	return noAction
}

func approvedPullRequest(pr *github_pr.PullRequest) bool {
	return github_pr.HasLabel(pr, github_issue.Claimed)
}
