package github_pr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flock/tools/github/github_client"
	"flock/tools/github/issue_reference"
)

func TestServiceTransfersMilestone(t *testing.T) {
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73,"state":"open","milestone":{"number":3}}`)
		case "PATCH /repos/flock-php/flock/issues/87", "PATCH /repos/flock-php/flock/issues/73":
			writes = append(writes, r.URL.Path+" "+string(body))
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	transferred, err := New(client).TransferIssueMilestone("flock-php", "flock", 87, 73)
	if err != nil || !transferred {
		t.Fatalf("TransferIssueMilestone() = %t, %v", transferred, err)
	}
	got := strings.Join(writes, "\n")
	if !strings.Contains(got, `/issues/87 {"milestone":3}`) || !strings.Contains(got, `/issues/73 {"milestone":null}`) {
		t.Fatalf("writes = %s", got)
	}
	if strings.Contains(got, `"state"`) {
		t.Fatalf("milestone transfer changed issue state: %s", got)
	}
}

func TestServiceListsPaginatedClosingIssuesReferences(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "closingIssuesReferences(first: 100, after: $cursor)") || strings.Contains(string(body), "excludeUserLinked") {
			t.Fatalf("query does not request all Development links: %s", body)
		}
		if strings.Contains(string(body), `"cursor":null`) {
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":73,"milestone":{"number":3},"repository":{"name":"flock","owner":{"login":"flock-php"}}}],"pageInfo":{"hasNextPage":true,"endCursor":"next-page"}}}}}}`)
			return
		}
		if !strings.Contains(string(body), `"cursor":"next-page"`) {
			t.Fatalf("second request cursor = %s", body)
		}
		_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":74,"milestone":null,"repository":{"name":"flock","owner":{"login":"flock-php"}}}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}}`)
	}))
	defer server.Close()

	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	issues, err := New(client).ListLinkedIssues("flock-php", "flock", 87)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 || issues[0].Owner != "flock-php" || issues[0].Repository != "flock" || issues[0].Number != 73 ||
		issues[0].MilestoneNumber == nil || *issues[0].MilestoneNumber != 3 || issues[1].MilestoneNumber != nil {
		t.Fatalf("ListLinkedIssues() = %+v", issues)
	}
}

func TestServicePropagatesLinkedIssueGraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"errors":[{"message":"linked issue access denied"}],"data":{"repository":{"pullRequest":{"closingIssuesReferences":{"nodes":[{"number":73}],"pageInfo":{"hasNextPage":false}}}}}}`)
	}))
	defer server.Close()

	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(client).ListLinkedIssues("flock-php", "flock", 87); err == nil || !strings.Contains(err.Error(), "linked issue access denied") {
		t.Fatalf("ListLinkedIssues() error = %v", err)
	}
}

func TestServiceGetsPullRequestsFilesAndUpdates(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/pulls/87":
			_, _ = io.WriteString(w, `{"number":87,"state":"open","labels":[{"name":"claimed"}]}`)
		case "GET /repos/flock-php/flock/pulls/87/files":
			if r.URL.Query().Get("page") == "2" {
				_, _ = io.WriteString(w, `[{"filename":"second.go"}]`)
				return
			}
			w.Header().Set("Link", "<"+server.URL+"/repos/flock-php/flock/pulls/87/files?page=2>; rel=\"next\"")
			_, _ = io.WriteString(w, `[{"filename":"first.go"}]`)
		case "POST /repos/flock-php/flock/issues/87/comments", "PATCH /repos/flock-php/flock/pulls/87":
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	service := New(client)
	pullRequest, err := service.Get("flock-php", "flock", 87)
	if err != nil || pullRequest.Number != 87 {
		t.Fatalf("Get() = %+v, %v", pullRequest, err)
	}
	if !HasLabel(pullRequest, "claimed") {
		t.Fatal("Get() did not return the claimed label")
	}
	files, err := service.ListFiles("flock-php", "flock", 87)
	if err != nil || len(files) != 2 {
		t.Fatalf("ListFiles() = %v, %v", files, err)
	}
	if err := service.CreateComment("flock-php", "flock", 87, "message"); err != nil {
		t.Fatal(err)
	}
	if err := service.SetState("flock-php", "flock", 87, "closed"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceGetsMergeQueueEnqueuer(t *testing.T) {
	for _, tt := range []struct {
		name    string
		body    string
		want    string
		wantErr string
	}{
		{name: "enqueued", body: `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"maintainer"}}}}}}`, want: "maintainer"},
		{name: "not enqueued", body: `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":null}}}}`, wantErr: "pull request #87 is not in the merge queue"},
		{name: "graphql error wins over partial data", body: `{"errors":[{"message":"merge queue access denied"}],"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"maintainer"}}}}}}`, wantErr: "get merge queue enqueuer for pull request #87: merge queue access denied"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			client, err := github_client.New(server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			got, err := New(client).GetMergeQueueEnqueuer("flock-php", "flock", 87)
			if tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr) {
				t.Fatalf("GetMergeQueueEnqueuer() error = %v, want %q", err, tt.wantErr)
			}
			if tt.wantErr == "" && (err != nil || got != tt.want) {
				t.Fatalf("GetMergeQueueEnqueuer() = %q, %v", got, err)
			}
		})
	}
}

func TestServiceDetectsGateClosedPullRequests(t *testing.T) {
	for _, tt := range []struct {
		name     string
		comments string
		want     bool
	}{
		{name: "gate closed", comments: `[{"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:closed -->","created_at":"2026-01-01T00:00:00Z"}]`, want: true},
		{name: "no marker", comments: `[]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/flock-php/flock/issues/87/comments":
					_, _ = io.WriteString(w, tt.comments)
				case "/repos/flock-php/flock/issues/87/events":
					_, _ = io.WriteString(w, `[{"event":"closed","actor":{"login":"github-actions[bot]"},"created_at":"2026-01-01T00:00:00Z"}]`)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := github_client.New(server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			got, err := New(client).GateClosed("flock-php", "flock", 87)
			if err != nil || got != tt.want {
				t.Fatalf("GateClosed() = %t, %v", got, err)
			}
		})
	}
}

func TestServiceReadsBotAuthoredClaimRecords(t *testing.T) {
	for _, tt := range []struct {
		name      string
		comments  string
		wantIssue issue_reference.Reference
		wantFound bool
	}{
		{name: "structured bot claim", comments: `[{"id":4,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=flock#73 author=contributor -->"}]`, wantIssue: issue_reference.Reference{Repository: "flock", Number: 73}, wantFound: true},
		{name: "user marker", comments: `[{"id":4,"user":{"login":"contributor"},"body":"<!-- pr-gate:claim issue=flock#73 author=contributor -->"}]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/flock-php/flock/issues/87/comments" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = io.WriteString(w, tt.comments)
			}))
			defer server.Close()

			client, err := github_client.New(server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			got, err := New(client).FindClaimRecord("flock-php", "flock", 87)
			if err != nil || (got != nil) != tt.wantFound {
				t.Fatalf("FindClaimRecord() = %+v, %v", got, err)
			}
			if got != nil && got.Issue != tt.wantIssue {
				t.Fatalf("record issue = %+v, want %+v", got.Issue, tt.wantIssue)
			}
		})
	}
}

func TestServiceCreatesReusesAndDeletesClaimRecords(t *testing.T) {
	var comments = 0
	var deleted int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/87/comments":
			if comments == 0 {
				_, _ = io.WriteString(w, `[]`)
				return
			}
			_, _ = io.WriteString(w, `[{"id":12,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim issue=flock#73 author=contributor -->"}]`)
		case "POST /repos/flock-php/flock/issues/87/comments":
			comments++
			_, _ = io.WriteString(w, `{"id":12}`)
		case "DELETE /repos/flock-php/flock/issues/comments/12":
			deleted = 12
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	service := New(client)
	issue := issue_reference.Reference{Repository: "flock", Number: 73}
	created, err := service.CreateClaimRecord("flock-php", "flock", 87, issue, "contributor")
	if err != nil || created.CommentID != 12 {
		t.Fatalf("CreateClaimRecord() = %+v, %v", created, err)
	}
	reused, err := service.CreateClaimRecord("flock-php", "flock", 87, issue, "contributor")
	if err != nil || reused.CommentID != 12 || comments != 1 {
		t.Fatalf("reused record = %+v, %v; comments = %d", reused, err, comments)
	}
	if err := service.DeleteClaimRecord("flock-php", "flock", created); err != nil || deleted != 12 {
		t.Fatalf("DeleteClaimRecord() = %v, deleted = %d", err, deleted)
	}
}

func TestClaimRecordTransitionForSelectsIdempotentWrites(t *testing.T) {
	issue := issue_reference.Reference{Repository: "flock", Number: 73}
	for _, tt := range []struct {
		name   string
		record *ClaimRecord
		issue  issue_reference.Reference
		author string
		want   claimRecordTransition
	}{
		{name: "missing record", issue: issue, author: "contributor", want: createClaimRecord},
		{name: "matching record", record: &ClaimRecord{Issue: issue, Author: "contributor"}, issue: issue, author: "contributor", want: reuseClaimRecord},
		{name: "different issue", record: &ClaimRecord{Issue: issue, Author: "contributor"}, issue: issue_reference.Reference{Repository: "flock", Number: 74}, author: "contributor", want: createClaimRecord},
		{name: "different author", record: &ClaimRecord{Issue: issue, Author: "another"}, issue: issue, author: "contributor", want: createClaimRecord},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := claimRecordTransitionFor(tt.record, tt.issue, tt.author); got != tt.want {
				t.Fatalf("claimRecordTransitionFor() = %d, want %d", got, tt.want)
			}
		})
	}
}
