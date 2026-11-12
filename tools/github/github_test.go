package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubAdapterIssueAndMergeCommands(t *testing.T) {
	var canceled []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/collaborators/contributor/permission":
			_, _ = io.WriteString(w, `{"permission":"write","role_name":"maintain"}`)
		case "POST /graphql":
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "mergeQueueEntry") {
				_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"contributor"}}}}}}`)
				return
			}
			_, _ = io.WriteString(w, `{"data":{}}`)
		case "GET /repos/flock-php/flock/actions/runs":
			_, _ = io.WriteString(w, `{"workflow_runs":[{"id":1,"status":"queued"},{"id":2,"status":"completed"}]}`)
		case "POST /repos/flock-php/flock/actions/runs/1/cancel":
			canceled = append(canceled, r.URL.Path)
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	adapter, err := newGitHubAdapter(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.execute("flock-php", "flock", command{kind: commandReadRepositoryPermission, username: "contributor"})
	if err != nil || result.permission != "write" {
		t.Fatalf("permission = %+v, %v", result, err)
	}
	result, err = adapter.execute("flock-php", "flock", command{kind: commandReadMergeQueueEnqueuer, pullRequestNumber: 123})
	if err != nil || result.enqueuer != "contributor" {
		t.Fatalf("enqueuer = %+v, %v", result, err)
	}
	if _, err := adapter.execute("flock-php", "flock", command{kind: commandCancelOtherRuns, headSHA: "abc", runID: 2}); err != nil {
		t.Fatal(err)
	}
	if len(canceled) != 1 || canceled[0] != "/repos/flock-php/flock/actions/runs/1/cancel" {
		t.Fatalf("canceled = %v", canceled)
	}
}
