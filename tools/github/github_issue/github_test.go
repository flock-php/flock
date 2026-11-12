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

func TestServiceIgnoresMissingClaimedLabel(t *testing.T) {
	service := New(nil)
	if err := service.RemoveClaimed("flock-php", "flock", &Issue{Number: 73}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceIgnoresMissingClaimedLabelResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.EscapedPath() != "/repos/flock-php/flock/issues/73/labels/claimed" {
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
	issue := &Issue{Number: 73, Labels: []Label{{Name: Claimed}}}
	if err := New(client).RemoveClaimed("flock-php", "flock", issue); err != nil {
		t.Fatalf("RemoveClaimed() = %v, want nil", err)
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
	issue.Labels = []Label{{Name: Claimed}}
	if err := service.RemoveClaimed("flock-php", "flock", issue); err == nil {
		t.Fatal("RemoveClaimed() returned nil error")
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

func TestIssueApprovalForClassifiesCurrentIssueState(t *testing.T) {
	tests := []struct {
		name  string
		issue *Issue
		want  IssueApprovalState
	}{
		{name: "missing issue", want: IssueApprovalDenied},
		{name: "closed issue", issue: &Issue{State: "closed"}, want: IssueApprovalDenied},
		{name: "pull request", issue: &Issue{State: "open", IsPullRequest: true}, want: IssueApprovalDenied},
		{name: "assigned author", issue: &Issue{State: "open", Assignees: []User{{Login: "contributor"}}}, want: IssueApprovalAssignedAuthor},
		{name: "assigned other author", issue: &Issue{State: "open", Assignees: []User{{Login: "other"}}, Labels: []Label{{Name: HelpWanted}}}, want: IssueApprovalDenied},
		{name: "unassigned help wanted", issue: &Issue{State: "open", Labels: []Label{{Name: HelpWanted}}}, want: IssueApprovalUnassignedHelpWanted},
		{name: "unassigned without help wanted", issue: &Issue{State: "open"}, want: IssueApprovalDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IssueApprovalFor(tt.issue, "contributor"); got != tt.want {
				t.Fatalf("IssueApprovalFor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestClaimOwnershipForClassifiesMarkerState(t *testing.T) {
	claim := &Claim{PullRequestNumber: 87, Author: "contributor"}
	tests := []struct {
		name   string
		issue  *Issue
		claim  *Claim
		author string
		pr     int
		want   ClaimOwnershipState
	}{
		{name: "no marker", want: ClaimOwnershipNone},
		{name: "owner on unassigned issue", claim: claim, author: "contributor", pr: 87, want: ClaimOwnershipThisPullRequest},
		{name: "owner on assigned issue", issue: &Issue{Assignees: []User{{Login: "contributor"}}}, claim: claim, author: "contributor", pr: 87, want: ClaimOwnershipThisPullRequest},
		{name: "same author different pull request", claim: claim, author: "contributor", pr: 86, want: ClaimOwnershipAnotherPullRequest},
		{name: "another pull request", claim: claim, author: "other", pr: 86, want: ClaimOwnershipAnotherPullRequest},
		{name: "additional assignee supersedes marker", issue: &Issue{Assignees: []User{{Login: "contributor"}, {Login: "maintainer"}}}, claim: claim, author: "contributor", pr: 87, want: ClaimOwnershipSuperseded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClaimOwnershipFor(tt.issue, tt.claim, tt.author, tt.pr); got != tt.want {
				t.Fatalf("ClaimOwnershipFor() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAuthorizationForCoversTheNamedStateMatrix(t *testing.T) {
	tests := []struct {
		name       string
		issueState IssueApprovalState
		claimState ClaimOwnershipState
		want       AuthorizationState
	}{
		{"denied without marker", IssueApprovalDenied, ClaimOwnershipNone, AuthorizationDenied},
		{"denied with superseded marker", IssueApprovalDenied, ClaimOwnershipSuperseded, AuthorizationDenied},
		{"assigned without marker", IssueApprovalAssignedAuthor, ClaimOwnershipNone, AuthorizationMaintainerAssignment},
		{"assigned with superseded marker", IssueApprovalAssignedAuthor, ClaimOwnershipSuperseded, AuthorizationMaintainerAssignment},
		{"assigned owner", IssueApprovalAssignedAuthor, ClaimOwnershipThisPullRequest, AuthorizationAutomaticClaimOwner},
		{"assigned conflict", IssueApprovalAssignedAuthor, ClaimOwnershipAnotherPullRequest, AuthorizationAutomaticClaimConflict},
		{"unassigned without marker", IssueApprovalUnassignedHelpWanted, ClaimOwnershipNone, AuthorizationCreateAutomaticClaim},
		{"unassigned owner", IssueApprovalUnassignedHelpWanted, ClaimOwnershipThisPullRequest, AuthorizationAutomaticClaimOwner},
		{"unassigned conflict", IssueApprovalUnassignedHelpWanted, ClaimOwnershipAnotherPullRequest, AuthorizationAutomaticClaimConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AuthorizationFor(tt.issueState, tt.claimState)
			if err != nil || got != tt.want {
				t.Fatalf("AuthorizationFor() = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}

func TestAuthorizationForRejectsUnknownStates(t *testing.T) {
	for _, tt := range []struct {
		name       string
		issueState IssueApprovalState
		claimState ClaimOwnershipState
	}{
		{"unknown issue state", IssueApprovalState(99), ClaimOwnershipNone},
		{"unknown claim state", IssueApprovalDenied, ClaimOwnershipState(99)},
		{"unsupported unassigned superseded state", IssueApprovalUnassignedHelpWanted, ClaimOwnershipSuperseded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AuthorizationFor(tt.issueState, tt.claimState)
			if err == nil || got != AuthorizationDenied {
				t.Fatalf("AuthorizationFor() = %d, %v; want denied with error", got, err)
			}
		})
	}
}
