package slo

import (
	"context"

	"github.com/google/go-github/v93/github"
)

func listOpenIssues(ctx context.Context, client *github.Client, owner, repository string) ([]*github.Issue, error) {
	options := &github.IssueListByRepoOptions{State: "open", ListOptions: github.ListOptions{PerPage: 100}}
	var result []*github.Issue
	for {
		items, response, err := client.Issues.ListByRepo(ctx, owner, repository, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response == nil || response.NextPage == 0 {
			return result, nil
		}
		options.ListOptions.Page = response.NextPage
	}
}

func listOpenPullRequests(ctx context.Context, client *github.Client, owner, repository string) ([]*github.PullRequest, error) {
	options := &github.PullRequestListOptions{State: "open", ListOptions: github.ListOptions{PerPage: 100}}
	var result []*github.PullRequest
	for {
		items, response, err := client.PullRequests.List(ctx, owner, repository, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response == nil || response.NextPage == 0 {
			return result, nil
		}
		options.ListOptions.Page = response.NextPage
	}
}

func listComments(ctx context.Context, client *github.Client, owner, repository string, number int) ([]*github.IssueComment, error) {
	options := &github.IssueListCommentsOptions{PerPage: 100}
	var result []*github.IssueComment
	for {
		items, response, err := client.Issues.ListComments(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response == nil || response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func listIssueEvents(ctx context.Context, client *github.Client, owner, repository string, number int) ([]*github.IssueEvent, error) {
	options := &github.ListOptions{PerPage: 100}
	var result []*github.IssueEvent
	for {
		items, response, err := client.Issues.ListIssueEvents(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response == nil || response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func listReviews(ctx context.Context, client *github.Client, owner, repository string, number int) ([]*github.PullRequestReview, error) {
	options := &github.ListOptions{PerPage: 100}
	var result []*github.PullRequestReview
	for {
		items, response, err := client.PullRequests.ListReviews(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response == nil || response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}

func listCommits(ctx context.Context, client *github.Client, owner, repository string, number int) ([]*github.RepositoryCommit, error) {
	options := &github.ListOptions{PerPage: 100}
	var result []*github.RepositoryCommit
	for {
		items, response, err := client.PullRequests.ListCommits(ctx, owner, repository, number, options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if response == nil || response.NextPage == 0 {
			return result, nil
		}
		options.Page = response.NextPage
	}
}
