package pr_review

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-github/v93/github"
)

type requestLog struct {
	method string
	path   string
}

func testClient(t *testing.T, handler http.Handler) (*github.Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	baseURL.Path += "/"
	base := baseURL.String()
	client, err := github.NewClient(github.WithAuthToken("token"), github.WithURLs(&base, nil))
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return client, server.Close
}

func writeTestJSON(t *testing.T, writer http.ResponseWriter, value string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, value)
}

func testReviewEvent(action, sender string) []byte {
	event := map[string]any{
		"action": action,
		"pull_request": map[string]any{
			"number": 1,
		},
		"sender": map[string]any{
			"login": sender,
		},
	}
	data, _ := json.Marshal(event)
	return data
}

func testWorkflowRunEvent(conclusion, sourceEvent, workflowName, headSHA string, pullRequestNumbers ...int) []byte {
	return testWorkflowRunEventFor("", "", conclusion, sourceEvent, workflowName, headSHA, pullRequestNumbers...)
}

func testWorkflowRunEventFor(headOwner, headBranch, conclusion, sourceEvent, workflowName, headSHA string, pullRequestNumbers ...int) []byte {
	pullRequests := make([]map[string]any, 0, len(pullRequestNumbers))
	for _, number := range pullRequestNumbers {
		pullRequests = append(pullRequests, map[string]any{"number": number})
	}
	workflowRun := map[string]any{
		"id":            42,
		"name":          workflowName,
		"event":         sourceEvent,
		"conclusion":    conclusion,
		"head_sha":      headSHA,
		"pull_requests": pullRequests,
	}
	if headOwner != "" || headBranch != "" {
		workflowRun["head_branch"] = headBranch
		workflowRun["head_repository"] = map[string]any{
			"name":  "repo",
			"owner": map[string]any{"login": headOwner},
		}
	}
	event := map[string]any{
		"action":       "completed",
		"workflow_run": workflowRun,
	}
	data, _ := json.Marshal(event)
	return data
}

func writeReviewSignal(t *testing.T, action string, number int, reviewID int64, state, commitID string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pr-review-event.json")
	event := map[string]any{
		"action": action,
		"pull_request": map[string]any{
			"number": number,
		},
		"review": map[string]any{
			"id":           reviewID,
			"state":        state,
			"commit_id":    commitID,
			"submitted_at": "2026-11-21T10:00:00Z",
		},
	}
	data, _ := json.Marshal(event)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func respondPresentLabel(t *testing.T, writer http.ResponseWriter, request *http.Request) bool {
	t.Helper()
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/repos/owner/repo/labels/") {
		writeTestJSON(t, writer, `{"name":"present"}`)
		return true
	}
	return false
}

func containsLog(logs []requestLog, method, path string) bool {
	for _, log := range logs {
		if log.method == method && log.path == path {
			return true
		}
	}
	return false
}

func countLog(logs []requestLog, method, path string) int {
	count := 0
	for _, log := range logs {
		if log.method == method && log.path == path {
			count++
		}
	}
	return count
}
