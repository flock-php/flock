// Package issue_triage labels new issues: by the platforms a bug report names, and for maintainer
// review when the author has no repository access.
package issue_triage

import (
	"strings"

	"github.com/google/go-github/v93/github"
)

type Service interface {
	Permission(owner, repository, username string) (string, error)
	AddLabel(owner, repository string, number int, label string) error
}

// platformLabels maps the bug report form's "Platform" options to their labels.
var platformLabels = map[string]string{
	"macOS":   "os: mac",
	"Windows": "os: windows",
	"Linux":   "os: linux",
}

func Run(api Service, owner, repository string, issue *github.Issue) error {
	for _, label := range platforms(issue.GetBody()) {
		if err := api.AddLabel(owner, repository, issue.GetNumber(), label); err != nil {
			return err
		}
	}

	permission, err := api.Permission(owner, repository, issue.GetUser().GetLogin())
	if err != nil {
		return err
	}
	if bypassesTriage(issue, permission) {
		return nil
	}
	return api.AddLabel(owner, repository, issue.GetNumber(), "needs triage")
}

func bypassesTriage(issue *github.Issue, permission string) bool {
	return issue.GetUser().GetType() == "Bot" || issue.GetAuthorAssociation() == "OWNER" || issue.GetAuthorAssociation() == "MEMBER" || issue.GetAuthorAssociation() == "COLLABORATOR" || permission == "admin" || permission == "write"
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
