// Command github applies Flock's pull request, merge queue and issue-triage policy in GitHub Actions.
package main

import (
	"errors"
	"fmt"
	"os"

	"flock/tools/github/github_client"
	"flock/tools/github/github_issue"
	"flock/tools/github/github_merge_group"
	"flock/tools/github/github_pr"
	"flock/tools/github/github_repository"
	"flock/tools/github/issue_triage"
	"flock/tools/github/merge_gate"
	"flock/tools/github/pr_assignment"
	"flock/tools/github/pr_gate"
	"flock/tools/github/repository"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "pr" && os.Args[1] != "issue" && os.Args[1] != "merge") {
		fail(errors.New("usage: github pr | issue | merge"))
	}
	owner, repo, err := repository.Parse(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		fail(err)
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fail(errors.New("GITHUB_TOKEN is not set"))
	}
	event, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		fail(err)
	}
	client, err := github_client.New(apiURL(), token)
	if err != nil {
		fail(err)
	}
	issues := github_issue.New(client)
	pullRequests := github_pr.New(client)
	repositories := github_repository.New(client)

	switch os.Args[1] {
	case "pr":
		service := PullRequestEventService{
			Assignment: pr_assignment.Dependencies{Issues: issues},
			Gate:       pr_gate.Dependencies{Issues: issues, PullRequests: pullRequests},
			Milestones: pullRequests,
		}
		err = service.Run(owner, repo, event)
	case "issue":
		service := IssueEventService{
			Assignment: issues,
			Triage:     issue_triage.Dependencies{Issues: issues, Repositories: repositories},
		}
		err = service.Run(owner, repo, event)
	case "merge":
		service := MergeGroupEventService{
			Gate: merge_gate.Dependencies{MergeGroups: github_merge_group.New(client), PullRequests: pullRequests, Repositories: repositories},
		}
		err = service.Run(owner, repo, event)
	}
	if err != nil {
		fail(err)
	}
}

func apiURL() string {
	if value := os.Getenv("GITHUB_API_URL"); value != "" {
		return value
	}
	return "https://api.github.com"
}

func fail(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "github:", err)
	os.Exit(1)
}
