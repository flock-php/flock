package github_pr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flock/tools/github/github_client"
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
		_, _ = io.WriteString(w, `{"errors":[{"message":"linked issue access denied"}]}`)
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
		name string
		body string
		want string
		fail bool
	}{
		{name: "enqueued", body: `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":{"enqueuer":{"login":"maintainer"}}}}}}`, want: "maintainer"},
		{name: "not enqueued", body: `{"data":{"repository":{"pullRequest":{"mergeQueueEntry":null}}}}`, fail: true},
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
			if tt.fail && err == nil {
				t.Fatal("GetMergeQueueEnqueuer() returned nil error")
			}
			if !tt.fail && (err != nil || got != tt.want) {
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

func TestServiceDetectsBotAuthoredClaimComments(t *testing.T) {
	for _, tt := range []struct {
		name     string
		comments string
		want     bool
	}{
		{name: "bot claim", comments: `[{"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim -->"}]`, want: true},
		{name: "user marker", comments: `[{"user":{"login":"contributor"},"body":"<!-- pr-gate:claim -->"}]`},
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
			got, err := New(client).ClaimCommentExists("flock-php", "flock", 87)
			if err != nil || got != tt.want {
				t.Fatalf("ClaimCommentExists() = %t, %v", got, err)
			}
		})
	}
}
