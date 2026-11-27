package contribution

import (
	"errors"
	"net/http"

	"github.com/google/go-github/v93/github"
)

func IsNotFound(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) && response.Response != nil && response.Response.StatusCode == http.StatusNotFound
}

func IsOutsideContributor(userType, authorAssociation string) bool {
	return userType != "Bot" && authorAssociation != "OWNER" && authorAssociation != "MEMBER" && authorAssociation != "COLLABORATOR"
}

func IsMaintainerRole(role string) bool {
	return role == "admin" || role == "maintain" || role == "write"
}

func CanMergeRole(role string) bool {
	return role == "admin" || role == "maintain"
}

func HasAssignedClaimAuthor(item *Issue, claims []Claim) bool {
	if item == nil {
		return false
	}
	for _, claim := range claims {
		if item.HasAssignee(claim.Author) {
			return true
		}
	}
	return false
}

func IssueApprovalFor(item *Issue, author string) bool {
	if item == nil || item.IsPullRequest || item.State != "open" {
		return false
	}
	if len(item.Assignees) != 0 {
		return item.HasAssignee(author)
	}
	return item.HasLabel(HelpWantedLabel)
}
