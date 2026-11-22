package pr_review

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAppliesRequestedLabelTransitions(t *testing.T) {
	tests := []struct {
		name        string
		action      string
		sender      string
		authorType  string
		association string
		state       string
		base        string
		labels      string
		wantWrites  []requestLog
	}{
		{
			name:        "outside opening",
			action:      "opened",
			sender:      "author",
			association: "CONTRIBUTOR",
			state:       "open",
			base:        "main",
			labels:      "[]",
			wantWrites:  []requestLog{{"POST", "/repos/owner/repo/issues/1/labels"}},
		},
		{
			name:        "bypassing opening",
			action:      "opened",
			sender:      "author",
			association: "OWNER",
			state:       "open",
			base:        "main",
			labels:      "[]",
		},
		{
			name:        "bypassing review request",
			action:      "review_requested",
			sender:      "maintainer",
			association: "OWNER",
			state:       "open",
			base:        "main",
			labels:      "[]",
			wantWrites:  []requestLog{{"POST", "/repos/owner/repo/issues/1/labels"}},
		},
		{
			name:        "author re-request",
			action:      "review_requested",
			sender:      "author",
			association: "CONTRIBUTOR",
			state:       "open",
			base:        "main",
			labels:      `[{"name":"pr: changes requested"}]`,
			wantWrites: []requestLog{
				{"POST", "/repos/owner/repo/issues/1/labels"},
				{"DELETE", "/repos/owner/repo/issues/1/labels/pr: changes requested"},
			},
		},
		{
			name:        "non-author request",
			action:      "review_requested",
			sender:      "maintainer",
			association: "CONTRIBUTOR",
			state:       "open",
			base:        "main",
			labels:      `[{"name":"pr: changes requested"}]`,
		},
		{
			name:        "other base",
			action:      "opened",
			sender:      "author",
			association: "CONTRIBUTOR",
			state:       "open",
			base:        "release",
			labels:      "[]",
		},
		{
			name:        "closed pull request",
			action:      "opened",
			sender:      "author",
			association: "CONTRIBUTOR",
			state:       "closed",
			base:        "main",
			labels:      "[]",
		},
		{
			name:        "already correct labels",
			action:      "review_requested",
			sender:      "author",
			association: "CONTRIBUTOR",
			state:       "open",
			base:        "main",
			labels:      `[{"name":"pr: review requested"}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs []requestLog
			client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logs = append(logs, requestLog{r.Method, r.URL.Path})
				if respondPresentLabel(t, w, r) {
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/owner/repo/pulls/1":
					writeTestJSON(t, w, `{"number":1,"state":"`+test.state+`","merged":false,"base":{"ref":"`+test.base+`"},"user":{"login":"author","type":"`+test.authorType+`"},"author_association":"`+test.association+`","labels":`+test.labels+`}`)
				case "POST /repos/owner/repo/issues/1/labels":
					writeTestJSON(t, w, `[]`)
				case "DELETE /repos/owner/repo/issues/1/labels/pr: changes requested":
					writeTestJSON(t, w, `{}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer closeServer()

			if err := Run(context.Background(), client, "owner", "repo", testReviewEvent(test.action, test.sender), ""); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if countLog(logs, "GET", "/repos/owner/repo/pulls/1") != 1 {
				t.Fatalf("pull request reads = %d, want 1; logs=%v", countLog(logs, "GET", "/repos/owner/repo/pulls/1"), logs)
			}
			var gotWrites []requestLog
			for _, log := range logs {
				if log.method == "POST" || log.method == "DELETE" {
					gotWrites = append(gotWrites, log)
				}
			}
			if len(gotWrites) != len(test.wantWrites) {
				t.Fatalf("writes = %v, want %v; logs=%v", gotWrites, test.wantWrites, logs)
			}
			for index, want := range test.wantWrites {
				if gotWrites[index] != want {
					t.Fatalf("write %d = %#v, want %#v; logs=%v", index, gotWrites[index], want, logs)
				}
			}
		})
	}
}

func TestRunReturnsContextualReadError(t *testing.T) {
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"read failed"}`, http.StatusInternalServerError)
	}))
	defer closeServer()

	err := Run(context.Background(), client, "owner", "repo", testReviewEvent("opened", "author"), "")
	if err == nil || !strings.Contains(err.Error(), "read pull request #1") {
		t.Fatalf("Run() error = %v, want contextual read error", err)
	}
}

func TestRunReturnsContextualAddError(t *testing.T) {
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if respondPresentLabel(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":[]}`)
		case "POST /repos/owner/repo/issues/1/labels":
			http.Error(w, `{"message":"write failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	err := Run(context.Background(), client, "owner", "repo", testReviewEvent("opened", "author"), "")
	if err == nil || !strings.Contains(err.Error(), `add "pr: review requested" label to pull request #1`) {
		t.Fatalf("Run() error = %v, want contextual add error", err)
	}
}

func TestRunReturnsContextualRemoveError(t *testing.T) {
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","merged":false,"base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":[{"name":"pr: review requested"},{"name":"pr: changes requested"}]}`)
		case "DELETE /repos/owner/repo/issues/1/labels/pr: changes requested":
			http.Error(w, `{"message":"write failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	err := Run(context.Background(), client, "owner", "repo", testReviewEvent("review_requested", "author"), "")
	if err == nil || !strings.Contains(err.Error(), `remove "pr: changes requested" label from pull request #1`) {
		t.Fatalf("Run() error = %v, want contextual remove error", err)
	}
}

func TestRunRetryAfterRemovalFailureConvergesFromLiveLabels(t *testing.T) {
	var logs []requestLog
	added := false
	removeAttempts := 0
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		if respondPresentLabel(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			labels := `[{"name":"pr: changes requested"}]`
			if added && removeAttempts > 1 {
				labels = `[{"name":"pr: review requested"}]`
			} else if added {
				labels = `[{"name":"pr: changes requested"},{"name":"pr: review requested"}]`
			}
			writeTestJSON(t, w, `{"number":1,"state":"open","merged":false,"base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":`+labels+`}`)
		case "POST /repos/owner/repo/issues/1/labels":
			added = true
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/1/labels/pr: changes requested":
			removeAttempts++
			if removeAttempts == 1 {
				http.Error(w, `{"message":"remove failed"}`, http.StatusInternalServerError)
				return
			}
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	event := testReviewEvent("review_requested", "author")
	if err := Run(context.Background(), client, "owner", "repo", event, ""); err == nil {
		t.Fatal("first Run() returned nil after removal failure")
	}
	if err := Run(context.Background(), client, "owner", "repo", event, ""); err != nil {
		t.Fatalf("retry Run() error = %v", err)
	}
	if count := countLog(logs, "POST", "/repos/owner/repo/issues/1/labels"); count != 1 {
		t.Fatalf("review label additions = %d, want 1; logs=%v", count, logs)
	}
	if count := countLog(logs, "DELETE", "/repos/owner/repo/issues/1/labels/pr: changes requested"); count != 2 {
		t.Fatalf("changes label removals = %d, want 2; logs=%v", count, logs)
	}
}

func TestRunRejectsMalformedEventBeforeReadingGitHub(t *testing.T) {
	if err := Run(context.Background(), nil, "owner", "repo", []byte("{"), ""); err == nil ||
		!strings.Contains(err.Error(), "parse pull request review event") {
		t.Fatalf("Run() error = %v, want contextual parse error", err)
	}
}

func TestRunAppliesChangesRequestedForMaintainerReviews(t *testing.T) {
	for _, role := range []string{"admin", "maintain"} {
		t.Run(role, func(t *testing.T) {
			var logs []requestLog
			client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logs = append(logs, requestLog{r.Method, r.URL.Path})
				if respondPresentLabel(t, w, r) {
					return
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/owner/repo/pulls/1":
					writeTestJSON(t, w, `{"number":1,"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`)
				case "GET /repos/owner/repo/pulls/1/reviews/99":
					writeTestJSON(t, w, `{"id":99,"state":"CHANGES_REQUESTED","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`)
				case "GET /repos/owner/repo/collaborators/reviewer/permission":
					writeTestJSON(t, w, `{"role_name":"`+role+`"}`)
				case "GET /repos/owner/repo/issues/1/events":
					writeTestJSON(t, w, `[]`)
				case "POST /repos/owner/repo/issues/1/labels":
					writeTestJSON(t, w, `[]`)
				case "DELETE /repos/owner/repo/issues/1/labels/pr: review requested":
					writeTestJSON(t, w, `{}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer closeServer()

			signalPath := writeReviewSignal(t, "submitted", 1, 99, "changes_requested", "sha")
			err := Run(context.Background(), client, "owner", "repo",
				testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1), signalPath)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			want := []requestLog{
				{"GET", "/repos/owner/repo/pulls/1"},
				{"GET", "/repos/owner/repo/pulls/1/reviews/99"},
				{"GET", "/repos/owner/repo/collaborators/reviewer/permission"},
				{"GET", "/repos/owner/repo/issues/1/events"},
				{"GET", "/repos/owner/repo/labels/pr: changes requested"},
				{"POST", "/repos/owner/repo/issues/1/labels"},
				{"DELETE", "/repos/owner/repo/issues/1/labels/pr: review requested"},
			}
			if len(logs) != len(want) {
				t.Fatalf("requests = %v, want %v", logs, want)
			}
			for index := range want {
				if logs[index] != want[index] {
					t.Fatalf("request %d = %#v, want %#v; logs=%v", index, logs[index], want[index], logs)
				}
			}
		})
	}
}

func TestRunRejectsInvalidReviewHandoffsWithoutWriting(t *testing.T) {
	tests := []struct {
		name          string
		event         []byte
		signalPath    string
		pullRequest   string
		review        string
		permission    string
		issueEvents   string
		wantReadCount int
	}{
		{
			name:          "write role",
			event:         testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest:   `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
			review:        `{"id":99,"state":"changes_requested","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`,
			permission:    `{"role_name":"write"}`,
			issueEvents:   `[]`,
			wantReadCount: 3,
		},
		{
			name:          "read role",
			event:         testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest:   `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
			review:        `{"id":99,"state":"changes_requested","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`,
			permission:    `{"role_name":"read"}`,
			issueEvents:   `[]`,
			wantReadCount: 3,
		},
		{
			name:          "contributor",
			event:         testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest:   `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
			review:        `{"id":99,"state":"changes_requested","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`,
			permission:    `{"role_name":"none"}`,
			issueEvents:   `[]`,
			wantReadCount: 3,
		},
		{
			name:        "wrong pull request",
			event:       testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 2),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
		},
		{
			name:          "api review commit differs",
			event:         testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest:   `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
			review:        `{"id":99,"state":"changes_requested","commit_id":"other","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`,
			permission:    `{"role_name":"admin"}`,
			issueEvents:   `[]`,
			wantReadCount: 2,
		},
		{
			name:        "wrong review state",
			event:       testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
			review:      `{"id":99,"state":"commented","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`,
			permission:  `{"role_name":"admin"}`,
			issueEvents: `[]`,
		},
		{
			name:        "failed source run",
			event:       testWorkflowRunEvent("failure", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
		},
		{
			name:        "wrong source event",
			event:       testWorkflowRunEvent("success", "push", signalWorkflowName, "sha", 1),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
		},
		{
			name:        "wrong workflow name",
			event:       testWorkflowRunEvent("success", "pull_request_review", "Other workflow", "sha", 1),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
		},
		{
			name:        "missing associated pull request",
			event:       testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha"),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
		},
		{
			name:        "multiple associated pull requests",
			event:       testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1, 2),
			pullRequest: `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
		},
		{
			name:          "later bot transition",
			event:         testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1),
			pullRequest:   `{"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`,
			review:        `{"id":99,"state":"changes_requested","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`,
			permission:    `{"role_name":"admin"}`,
			issueEvents:   `[{"event":"labeled","actor":{"login":"github-actions[bot]"},"label":{"name":"pr: review requested"},"created_at":"2026-11-21T11:00:00Z"}]`,
			wantReadCount: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs []requestLog
			client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logs = append(logs, requestLog{r.Method, r.URL.Path})
				switch r.Method + " " + r.URL.Path {
				case "GET /repos/owner/repo/pulls/1":
					writeTestJSON(t, w, `{"number":1,`+strings.TrimPrefix(test.pullRequest, "{"))
				case "GET /repos/owner/repo/pulls/2":
					writeTestJSON(t, w, `{"number":2,`+strings.TrimPrefix(test.pullRequest, "{"))
				case "GET /repos/owner/repo/pulls/1/reviews/99":
					writeTestJSON(t, w, test.review)
				case "GET /repos/owner/repo/collaborators/reviewer/permission":
					writeTestJSON(t, w, test.permission)
				case "GET /repos/owner/repo/issues/1/events":
					writeTestJSON(t, w, test.issueEvents)
				default:
					http.NotFound(w, r)
				}
			}))
			defer closeServer()
			signalPath := test.signalPath
			if test.name == "wrong pull request" {
				signalPath = writeReviewSignal(t, "submitted", 1, 99, "changes_requested", "sha")
			}
			if signalPath == "" && test.review != "" {
				signalPath = writeReviewSignal(t, "submitted", 1, 99, "changes_requested", "sha")
			}
			if err := Run(context.Background(), client, "owner", "repo", test.event, signalPath); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			for _, log := range logs {
				if log.method == "POST" || log.method == "DELETE" {
					t.Fatalf("unexpected write request: %v; logs=%v", log, logs)
				}
			}
			if test.wantReadCount != 0 && len(logs) != test.wantReadCount {
				t.Fatalf("read requests = %d, want %d; logs=%v", len(logs), test.wantReadCount, logs)
			}
		})
	}
}

func TestRunRetryAfterRequestedChangesRemovalFailureConverges(t *testing.T) {
	var logs []requestLog
	added := false
	removeAttempts := 0
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		if respondPresentLabel(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			labels := `[{"name":"pr: review requested"}]`
			if added && removeAttempts > 1 {
				labels = `[]`
			} else if added {
				labels = `[{"name":"pr: review requested"},{"name":"pr: changes requested"}]`
			}
			writeTestJSON(t, w, `{"number":1,"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":`+labels+`}`)
		case "GET /repos/owner/repo/pulls/1/reviews/99":
			writeTestJSON(t, w, `{"id":99,"state":"changes_requested","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`)
		case "GET /repos/owner/repo/collaborators/reviewer/permission":
			writeTestJSON(t, w, `{"role_name":"admin"}`)
		case "GET /repos/owner/repo/issues/1/events":
			writeTestJSON(t, w, `[]`)
		case "POST /repos/owner/repo/issues/1/labels":
			added = true
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/1/labels/pr: review requested":
			removeAttempts++
			if removeAttempts == 1 {
				http.Error(w, `{"message":"remove failed"}`, http.StatusInternalServerError)
				return
			}
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	signalPath := writeReviewSignal(t, "submitted", 1, 99, "changes_requested", "sha")
	event := testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1)
	if err := Run(context.Background(), client, "owner", "repo", event, signalPath); err == nil {
		t.Fatal("first Run() returned nil after removal failure")
	}
	if err := Run(context.Background(), client, "owner", "repo", event, signalPath); err != nil {
		t.Fatalf("retry Run() error = %v", err)
	}
	if count := countLog(logs, "POST", "/repos/owner/repo/issues/1/labels"); count != 1 {
		t.Fatalf("changes label additions = %d, want 1; logs=%v", count, logs)
	}
	if count := countLog(logs, "DELETE", "/repos/owner/repo/issues/1/labels/pr: review requested"); count != 2 {
		t.Fatalf("review label removals = %d, want 2; logs=%v", count, logs)
	}
}

func TestRunReturnsSignalReadAndParseErrors(t *testing.T) {
	event := testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1)
	missing := filepath.Join(t.TempDir(), "pr-review-event.json")
	err := Run(context.Background(), nil, "owner", "repo", event, missing)
	if err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), "pull request #1") ||
		!strings.Contains(err.Error(), "workflow run 42") {
		t.Fatalf("Run() error = %v, want a read error naming the path, pull request, and workflow run", err)
	}

	malformed := filepath.Join(t.TempDir(), "pr-review-event.json")
	if writeErr := os.WriteFile(malformed, []byte("{"), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	err = Run(context.Background(), nil, "owner", "repo", event, malformed)
	if err == nil || !strings.Contains(err.Error(), malformed) || !strings.Contains(err.Error(), "pull request #1") ||
		!strings.Contains(err.Error(), "workflow run 42") {
		t.Fatalf("Run() error = %v, want a parse error naming the path, pull request, and workflow run", err)
	}
}

func TestRunIgnoresParsedReviewThatDidNotRequestChanges(t *testing.T) {
	signalPath := writeReviewSignal(t, "submitted", 1, 99, "approved", "sha")
	err := Run(context.Background(), nil, "owner", "repo",
		testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "sha", 1), signalPath)
	if err != nil {
		t.Fatalf("Run() error = %v, want a successful no-op after the signal parses", err)
	}
}

func TestRunAppliesChangesRequestedWhenWorkflowHeadIsMergeCommit(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		if respondPresentLabel(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","merged":false,"base":{"ref":"main"},"head":{"sha":"sha"},"labels":[{"name":"pr: review requested"}]}`)
		case "GET /repos/owner/repo/pulls/1/reviews/99":
			writeTestJSON(t, w, `{"id":99,"state":"CHANGES_REQUESTED","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`)
		case "GET /repos/owner/repo/collaborators/reviewer/permission":
			writeTestJSON(t, w, `{"role_name":"admin"}`)
		case "GET /repos/owner/repo/issues/1/events":
			writeTestJSON(t, w, `[]`)
		case "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/1/labels/pr: review requested":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	signalPath := writeReviewSignal(t, "submitted", 1, 99, "changes_requested", "artifact-sha")
	err := Run(context.Background(), client, "owner", "repo",
		testWorkflowRunEvent("success", "pull_request_review", signalWorkflowName, "merge-sha", 1), signalPath)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !containsLog(logs, "POST", "/repos/owner/repo/issues/1/labels") ||
		!containsLog(logs, "DELETE", "/repos/owner/repo/issues/1/labels/pr: review requested") {
		t.Fatalf("requests = %v, want label writes despite the merge commit and artifact commit", logs)
	}
}

func TestRunAppliesChangesRequestedForForkHead(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		if r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/pulls" {
			query := r.URL.Query()
			if query.Get("head") != "forker:feature" || query.Get("base") != "main" || query.Get("state") != "open" {
				t.Fatalf("list query = %s", r.URL.RawQuery)
			}
			writeTestJSON(t, w, `[{"number":7,"state":"open","base":{"ref":"main"},"head":{"ref":"feature","sha":"sha","repo":{"owner":{"login":"forker"}}}}]`)
			return
		}
		if respondPresentLabel(t, w, r) {
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/7":
			writeTestJSON(t, w, `{"number":7,"state":"open","merged":false,"base":{"ref":"main"},"head":{"ref":"feature","sha":"sha","repo":{"owner":{"login":"forker"}}},"labels":[{"name":"pr: review requested"}]}`)
		case "GET /repos/owner/repo/pulls/7/reviews/99":
			writeTestJSON(t, w, `{"id":99,"state":"CHANGES_REQUESTED","commit_id":"sha","user":{"login":"reviewer"},"submitted_at":"2026-11-21T10:00:00Z"}`)
		case "GET /repos/owner/repo/collaborators/reviewer/permission":
			writeTestJSON(t, w, `{"role_name":"maintain"}`)
		case "GET /repos/owner/repo/issues/7/events":
			writeTestJSON(t, w, `[]`)
		case "POST /repos/owner/repo/issues/7/labels":
			writeTestJSON(t, w, `[]`)
		case "DELETE /repos/owner/repo/issues/7/labels/pr: review requested":
			writeTestJSON(t, w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	signalPath := writeReviewSignal(t, "submitted", 7, 99, "changes_requested", "artifact-sha")
	err := Run(context.Background(), client, "owner", "repo",
		testWorkflowRunEventFor("forker", "feature", "success", "pull_request_review", signalWorkflowName, "merge-sha"), signalPath)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !containsLog(logs, "GET", "/repos/owner/repo/pulls") ||
		!containsLog(logs, "POST", "/repos/owner/repo/issues/7/labels") ||
		!containsLog(logs, "DELETE", "/repos/owner/repo/issues/7/labels/pr: review requested") {
		t.Fatalf("requests = %v, want a fork head lookup and label writes", logs)
	}
}

func TestRunCreatesMissingLabelOnce(t *testing.T) {
	var logs []requestLog
	client, closeServer := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logs = append(logs, requestLog{r.Method, r.URL.Path})
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/owner/repo/pulls/1":
			writeTestJSON(t, w, `{"number":1,"state":"open","merged":false,"base":{"ref":"main"},"user":{"login":"author","type":"User"},"author_association":"CONTRIBUTOR","labels":[]}`)
		case "GET /repos/owner/repo/labels/pr: review requested":
			if countLog(logs, "POST", "/repos/owner/repo/labels") == 0 {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
				return
			}
			writeTestJSON(t, w, `{"name":"pr: review requested"}`)
		case "POST /repos/owner/repo/labels":
			writeTestJSON(t, w, `{"name":"pr: review requested"}`)
		case "POST /repos/owner/repo/issues/1/labels":
			writeTestJSON(t, w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer closeServer()

	event := testReviewEvent("opened", "author")
	if err := Run(context.Background(), client, "owner", "repo", event, ""); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := Run(context.Background(), client, "owner", "repo", event, ""); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if count := countLog(logs, "POST", "/repos/owner/repo/labels"); count != 1 {
		t.Fatalf("label creates = %d, want 1; logs=%v", count, logs)
	}
	if count := countLog(logs, "GET", "/repos/owner/repo/labels/pr: review requested"); count != 2 {
		t.Fatalf("label reads = %d, want 2; logs=%v", count, logs)
	}
}
