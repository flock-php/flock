// Command github applies Flock's pull request, merge queue and issue-triage policy in GitHub Actions.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "pr" && os.Args[1] != "issue" && os.Args[1] != "merge") {
		fail(errors.New("usage: github pr | issue | merge"))
	}
	owner, repo, err := parseRepository(os.Getenv("GITHUB_REPOSITORY"))
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
	adapter, err := newGitHubAdapter(apiURL(), token)
	if err != nil {
		fail(err)
	}
	runners := map[string]func(string, string, []byte, *githubAdapter) error{
		"pr":    runPR,
		"issue": runIssue,
		"merge": runMerge,
	}
	err = runners[os.Args[1]](owner, repo, event, adapter)
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
