package issue

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

type triageDecision uint8

const (
	triageNeedsTriage triageDecision = iota
	triageBypass
)

func triageDecisionFor(item contribution.Issue, permission string) triageDecision {
	if item.User.Type == "Bot" || item.AuthorAssociation == "OWNER" || item.AuthorAssociation == "MEMBER" ||
		item.AuthorAssociation == "COLLABORATOR" || permission == "admin" || permission == "write" {
		return triageBypass
	}
	return triageNeedsTriage
}

func Triage(ctx context.Context, client *github.Client, owner, repository string, data []byte) error {
	event, err := parseIssueEvent(data)
	if err != nil {
		return fmt.Errorf("parse issue event: %w", err)
	}
	if event.action != "opened" || event.issue.Number == 0 {
		return nil
	}
	for _, label := range issuePlatforms(event.issue.Body) {
		if _, _, err := client.Issues.AddLabelsToIssue(ctx, owner, repository, event.issue.Number, []string{label}); err != nil {
			return fmt.Errorf("add %q label to issue #%d: %w", label, event.issue.Number, err)
		}
	}
	permission, _, err := client.Repositories.GetPermissionLevel(ctx, owner, repository, event.issue.User.Login)
	if err != nil {
		return fmt.Errorf("read permission for @%s: %w", event.issue.User.Login, err)
	}
	return applyTriageDecision(ctx, client, owner, repository, event.issue, triageDecisionFor(event.issue, permission.GetPermission()))
}

func applyTriageDecision(ctx context.Context, client *github.Client, owner, repository string, item contribution.Issue, decision triageDecision) error {
	switch decision {
	case triageBypass:
		return nil
	case triageNeedsTriage:
		if _, _, err := client.Issues.AddLabelsToIssue(ctx, owner, repository, item.Number, []string{"needs triage"}); err != nil {
			return fmt.Errorf("add %q label to issue #%d: %w", "needs triage", item.Number, err)
		}
		return nil
	default:
		return fmt.Errorf("unknown triage decision %d", decision)
	}
}

var platformLabels = map[string]string{
	"macOS":   "os: mac",
	"Windows": "os: windows",
	"Linux":   "os: linux",
}

func issuePlatforms(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != "### Platform" {
			continue
		}
		for _, answer := range lines[index+1:] {
			answer = strings.TrimSpace(answer)
			if answer == "" {
				continue
			}
			var labels []string
			for platform := range strings.SplitSeq(answer, ",") {
				if label, ok := platformLabels[strings.TrimSpace(platform)]; ok {
					labels = append(labels, label)
				}
			}
			return labels
		}
	}
	return nil
}
