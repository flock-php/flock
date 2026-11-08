// Command github applies Flock's pull request, merge queue and issue-triage policy in GitHub Actions.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/github_service"
	"flock/tools/github/issue_triage"
	"flock/tools/github/merge_gate"
	"flock/tools/github/milestone_transfer"
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
	client, err := github_service.New(apiURL(), token)
	if err != nil {
		fail(err)
	}

	switch os.Args[1] {
	case "pr":
		err = runPullRequestEvent(client, owner, repo, event)
	case "issue":
		err = runIssueEvent(client, owner, repo, event)
	case "merge":
		err = runMergeGroupEvent(client, owner, repo, event)
	}
	if err != nil {
		fail(err)
	}
}

func runPullRequestEvent(api *github_service.Service, owner, repository string, data []byte) error {
	var event github.PullRequestEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	result, err := pr_gate.Run(api, owner, repository, pullRequestGateEvent(&event))
	if err != nil {
		return err
	}
	if result.ApprovedIssue == nil {
		return nil
	}
	return milestone_transfer.Run(api, owner, repository, event.GetPullRequest().GetNumber(), *result.ApprovedIssue)
}

func pullRequestGateEvent(event *github.PullRequestEvent) pr_gate.Event {
	changes := event.GetChanges()
	return pr_gate.Event{
		Action:     event.GetAction(),
		Number:     event.GetPullRequest().GetNumber(),
		BodyEdited: changes != nil && changes.Body != nil,
		BaseEdited: changes != nil && changes.Base != nil,
	}
}

func runIssueEvent(api *github_service.Service, owner, repository string, data []byte) error {
	var event github.IssuesEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	return issue_triage.Run(api, owner, repository, event.GetIssue())
}

func runMergeGroupEvent(api *github_service.Service, owner, repository string, data []byte) error {
	var event github.MergeGroupEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	runID, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	return merge_gate.Run(api, owner, repository, merge_gate.Event{
		HeadRef: event.GetMergeGroup().GetHeadRef(),
		HeadSHA: event.GetMergeGroup().GetHeadSHA(),
		RunID:   runID,
	})
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
