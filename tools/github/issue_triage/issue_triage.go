// Package issue_triage labels issues that need maintainer review.
package issue_triage

import "github.com/google/go-github/v93/github"

type Service interface {
	Permission(owner, repository, username string) (string, error)
	AddLabel(owner, repository string, number int, label string) error
}

func Run(api Service, owner, repository string, issue *github.Issue) error {
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
