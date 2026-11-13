// Command github applies Flock's pull request, merge queue and issue-triage policy in GitHub Actions.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/issue"
	"flock/tools/github/merge"
	"flock/tools/github/pr"
)

func main() {
	if !validCommand(os.Args[1:]) {
		fail(errors.New("usage: github pr release|gate|claim|milestone | issue triage|assignment | merge"))
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
	ctx := context.Background()
	client, err := newGitHubClient(apiURL(), token)
	if err != nil {
		fail(err)
	}
	switch os.Args[1] {
	case "pr":
		switch os.Args[2] {
		case "release":
			err = pr.Release(ctx, client, owner, repo, event)
		case "gate":
			err = pr.Gate(ctx, client, owner, repo, event)
		case "claim":
			err = pr.Claim(ctx, client, owner, repo, event)
		case "milestone":
			err = pr.Milestone(ctx, client, owner, repo, event)
		}
	case "issue":
		if os.Args[2] == "triage" {
			err = issue.Triage(ctx, client, owner, repo, event)
		} else {
			err = issue.Assignment(ctx, client, owner, repo, event)
		}
	case "merge":
		err = merge.Run(ctx, client, owner, repo, event, os.Getenv("GITHUB_RUN_ID"))
	}
	if err != nil {
		fail(err)
	}
}

func validCommand(args []string) bool {
	if len(args) == 2 && args[0] == "pr" {
		return args[1] == "release" || args[1] == "gate" || args[1] == "claim" || args[1] == "milestone"
	}
	if len(args) == 2 && args[0] == "issue" {
		return args[1] == "triage" || args[1] == "assignment"
	}
	return len(args) == 1 && args[0] == "merge"
}

func newGitHubClient(baseURL, token string) (*github.Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return github.NewClient(
		github.WithAuthToken(token),
		github.WithURLs(new(parsed.String()), nil),
	)
}

func parseRepository(value string) (string, string, error) {
	owner, name, ok := strings.Cut(value, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("GITHUB_REPOSITORY must be owner/repository, got %q", value)
	}
	return owner, name, nil
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
