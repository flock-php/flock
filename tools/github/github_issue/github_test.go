package github_issue

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flock/tools/github/github_client"
)

func TestServiceAssignsUserAndRemovesLabel(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.EscapedPath()+" "+string(body))
		switch r.Method + " " + r.URL.EscapedPath() {
		case "POST /repos/flock-php/flock/issues/73/assignees":
			_, _ = io.WriteString(w, `{"number":73,"assignees":[{"login":"contributor"}]}`)
		case "DELETE /repos/flock-php/flock/issues/73/assignees":
			_, _ = io.WriteString(w, `{}`)
		case "DELETE /repos/flock-php/flock/issues/73/labels/help%20wanted":
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
	service := New(client)
	assigned, err := service.AddAssignee("flock-php", "flock", 73, "contributor")
	if err != nil || !HasAssignee(assigned, "contributor") {
		t.Fatalf("AddAssignee() = %+v, %v", assigned, err)
	}
	if err := service.RemoveAssignee("flock-php", "flock", 73, "contributor"); err != nil {
		t.Fatal(err)
	}
	issue := &Issue{Number: 73, Labels: []Label{{Name: HelpWanted}}}
	if err := service.RemoveHelpWanted("flock-php", "flock", issue); err != nil {
		t.Fatal(err)
	}

	got := strings.Join(requests, "\n")
	if !strings.Contains(got, `POST /repos/flock-php/flock/issues/73/assignees {"assignees":["contributor"]}`) ||
		!strings.Contains(got, `DELETE /repos/flock-php/flock/issues/73/assignees {"assignees":["contributor"]}`) ||
		!strings.Contains(got, "DELETE /repos/flock-php/flock/issues/73/labels/help%20wanted ") {
		t.Fatalf("requests = %s", got)
	}
}

func TestServiceReturnsAssignmentResponseWithoutRequestedUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"number":73,"assignees":[]}`)
	}))
	defer server.Close()

	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	issue, err := New(client).AddAssignee("flock-php", "flock", 73, "contributor")
	if err != nil || HasAssignee(issue, "contributor") {
		t.Fatalf("AddAssignee() = %+v, %v", issue, err)
	}
}

func TestServiceIgnoresMissingHelpWantedLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/repos/flock-php/flock/issues/73/labels/help%20wanted" {
			t.Errorf("request = %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Label does not exist"}`)
	}))
	defer server.Close()

	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	issue := &Issue{Number: 73, Labels: []Label{{Name: HelpWanted}}}
	if err := New(client).RemoveHelpWanted("flock-php", "flock", issue); err != nil {
		t.Fatalf("RemoveHelpWanted() = %v, want nil", err)
	}
}

func TestServiceGetsIssueAndAddsLabel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /repos/flock-php/flock/issues/73":
			_, _ = io.WriteString(w, `{"number":73}`)
		case "POST /repos/flock-php/flock/issues/73/labels":
			_, _ = io.WriteString(w, `[]`)
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
	issue, err := service.Get("flock-php", "flock", 73)
	if err != nil || issue.Number != 73 {
		t.Fatalf("Get() = %+v, %v", issue, err)
	}
	if err := service.AddLabel("flock-php", "flock", 73, "needs triage"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceCreatesClaimedLabelWhenMissing(t *testing.T) {
	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.EscapedPath() {
		case "GET /repos/flock-php/flock/labels/claimed":
			w.WriteHeader(http.StatusNotFound)
		case "POST /repos/flock-php/flock/labels":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"name":"claimed"`) {
				t.Fatalf("label request = %s", body)
			}
			created = true
			_, _ = io.WriteString(w, `{"name":"claimed"}`)
		default:
			t.Errorf("request = %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if err := New(client).EnsureClaimedLabel("flock-php", "flock"); err != nil || !created {
		t.Fatalf("EnsureClaimedLabel() = %v, created = %t", err, created)
	}
}

func TestServiceCreatesAndFindsAutomatedClaims(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /repos/flock-php/flock/issues/73/comments":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "<!-- pr-gate:claim pr=87 author=contributor -->") ||
				!strings.Contains(string(body), "Issue claimed by @contributor in PR #87.") {
				t.Fatalf("claim comment = %s", body)
			}
			_, _ = io.WriteString(w, `{}`)
		case "GET /repos/flock-php/flock/issues/73/comments":
			if r.URL.Query().Get("page") == "2" {
				_, _ = io.WriteString(w, `[{"id":99,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=86 author=first-contributor -->"}]`)
				return
			}
			w.Header().Set("Link", "<"+server.URL+"/repos/flock-php/flock/issues/73/comments?page=2>; rel=\"next\"")
			_, _ = io.WriteString(w, `[
				{"id":1,"user":{"login":"contributor"},"body":"<!-- pr-gate:claim pr=1 author=forged -->"},
				{"id":2,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=0 author=invalid -->"},
				{"id":100,"user":{"login":"github-actions[bot]"},"body":"<!-- pr-gate:claim pr=87 author=contributor -->"}
			]`)
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
	if err := service.CreateClaim("flock-php", "flock", 73, 87, "contributor"); err != nil {
		t.Fatal(err)
	}
	claim, err := service.FindClaim("flock-php", "flock", 73)
	if err != nil || claim == nil || claim.PullRequestNumber != 86 || claim.Author != "first-contributor" {
		t.Fatalf("FindClaim() = %+v, %v", claim, err)
	}
}

func TestServicePropagatesIssueErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := github_client.New(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	service := New(client)
	if _, err := service.Get("flock-php", "flock", 73); err == nil {
		t.Fatal("Get() returned nil error")
	}
	if err := service.AddLabel("flock-php", "flock", 73, "needs triage"); err == nil {
		t.Fatal("AddLabel() returned nil error")
	}
	issue := &Issue{Number: 73, Labels: []Label{{Name: HelpWanted}}}
	if err := service.RemoveHelpWanted("flock-php", "flock", issue); err == nil {
		t.Fatal("RemoveHelpWanted() returned nil error")
	}
}

func TestRemoveHelpWantedSkipsIssuesWithoutTheLabel(t *testing.T) {
	service := New(nil)
	if err := service.RemoveHelpWanted("flock-php", "flock", &Issue{Number: 73}); err != nil {
		t.Fatal(err)
	}
}

func TestIssueHelpers(t *testing.T) {
	issue := &Issue{
		Assignees: []User{{Login: "contributor"}},
		Labels:    []Label{{Name: HelpWanted}, {Name: "bug"}},
	}
	if !HasHelpWanted(issue) {
		t.Fatal("HasHelpWanted() = false")
	}
	if !HasLabel(issue, "bug") || HasLabel(issue, "feature") {
		t.Fatal("HasLabel() returned an unexpected result")
	}
	if !HasAssignee(issue, "contributor") || HasAssignee(issue, "someone-else") {
		t.Fatal("HasAssignee() returned an unexpected result")
	}
}
