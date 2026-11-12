// Package issue_triage labels new issues by platform and whether they need maintainer review.
package issue_triage

import (
	"strings"

	"flock/tools/github/github_issue"
)

type UserPermissionReader interface {
	GetUserPermission(owner, repository, username string) (string, error)
}

type IssueLabelAdder interface {
	AddLabel(owner, repository string, number int, label string) error
}

var platformLabels = map[string]string{
	"macOS":   "os: mac",
	"Windows": "os: windows",
	"Linux":   "os: linux",
}

type Dependencies struct {
	Issues       IssueLabelAdder
	Repositories UserPermissionReader
}

func Execute(dependencies Dependencies, owner, repository string, issue *github_issue.Issue) error {
	if issue == nil {
		return nil
	}
	for _, label := range platforms(issue.Body) {
		if err := dependencies.Issues.AddLabel(owner, repository, issue.Number, label); err != nil {
			return err
		}
	}
	permission, err := dependencies.Repositories.GetUserPermission(owner, repository, issue.User.Login)
	if err != nil {
		return err
	}
	if bypassesTriage(issue, permission) {
		return nil
	}
	return dependencies.Issues.AddLabel(owner, repository, issue.Number, "needs triage")
}

func bypassesTriage(issue *github_issue.Issue, permission string) bool {
	if issue.User.Type == "Bot" {
		return true
	}
	switch issue.AuthorAssociation {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return permission == "admin" || permission == "write"
}

// platforms returns the labels for the platforms picked in an issue form's "Platform" field, which
// GitHub writes into the body as a "### Platform" heading followed by a comma-separated answer.
func platforms(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "### Platform" {
			continue
		}
		for _, answer := range lines[i+1:] {
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
