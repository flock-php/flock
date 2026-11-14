package pr

import (
	"context"
	"net/http"
	"testing"
)

func TestRunReleasesClaimAfterGateClosesStillLinkedPullRequest(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR"}`)
		case "GET /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `[{"id":10,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=repo#9 author=author -->"}]`)
		case "POST /graphql":
			writeTestJSON(t, w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":9,"repository":{"name":"repo","owner":{"login":"owner"}}}],"pageInfo":{"hasNextPage":false}}}}}}`)
		case "GET /repos/owner/repo/pulls/1/files":
			writeTestJSON(t, w, `[{"filename":"internal/file.go"}]`)
		case "GET /repos/owner/repo/issues/9":
			writeTestJSON(t, w, `{"number":9,"state":"open","labels":[{"name":"claimed"}],"assignees":[{"login":"maintainer"}]}`)
		case "GET /repos/owner/repo/issues/9/comments":
			writeTestJSON(t, w, `[{"id":11,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=1 author=author -->"}]`)
		case "POST /repos/owner/repo/issues/1/comments":
			writeTestJSON(t, w, `{"id":12}`)
		case "PATCH /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"closed"}`)
		case "DELETE /repos/owner/repo/issues/comments/11", "DELETE /repos/owner/repo/issues/comments/10":
			writeTestJSON(t, w, `{}`)
		case "DELETE /repos/owner/repo/issues/9/labels/claimed":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	if err := Run(context.Background(), client, "owner", "repo", testPullRequestEvent("synchronize", 1, false)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	closeIndex, releaseIndex := -1, -1
	for index, log := range logs {
		if log.method == "PATCH" && log.path == "/repos/owner/repo/pulls/1" {
			closeIndex = index
		}
		if log.method == "DELETE" && log.path == "/repos/owner/repo/issues/comments/10" {
			releaseIndex = index
		}
	}
	if closeIndex == -1 || releaseIndex == -1 || releaseIndex < closeIndex {
		t.Fatalf("claim record was not released after the gate close: %v", logs)
	}
}
