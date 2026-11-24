package slo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
)

func TestPlanPRReviewSkipsDraftsAndStartsAtReadyForReview(t *testing.T) {
	created := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	firstReady := created.Add(time.Hour)
	ready := created.Add(150 * time.Hour)
	client := githubTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/owner/repo/issues/9/timeline":
			fmt.Fprintf(w, `[{"event":"ready_for_review","created_at":%q},{"event":"convert_to_draft","created_at":%q},{"event":"ready_for_review","created_at":%q}]`,
				firstReady.Format(time.RFC3339), created.Add(2*time.Hour).Format(time.RFC3339), ready.Format(time.RFC3339))
		case "/repos/owner/repo/issues/10/timeline":
			io.WriteString(w, `[]`)
		default:
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
		}
	})
	current := &state{client: client, owner: "owner", repository: "repo", now: ready.Add(time.Hour), roles: map[string]string{}}
	draft := &github.PullRequest{Number: github.Ptr(8), Draft: github.Ptr(true), CreatedAt: &github.Timestamp{Time: created}}
	if err := current.planPRReview(context.Background(), draft, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("draft changes = %#v, want no reminder", current.changes)
	}

	converted := &github.PullRequest{Number: github.Ptr(9), CreatedAt: &github.Timestamp{Time: created}}
	if err := current.planPRReview(context.Background(), converted, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("converted draft changes = %#v, want no reminder until 48 hours after ready_for_review", current.changes)
	}

	current.now = ready.Add(50 * time.Hour)
	if err := current.planPRReview(context.Background(), converted, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || !current.changes[0].start.Equal(ready) || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("converted draft changes = %#v, want the 48-hour reminder from ready_for_review", current.changes)
	}

	current.changes = nil
	current.now = created.Add(100 * time.Hour)
	opened := &github.PullRequest{Number: github.Ptr(10), CreatedAt: &github.Timestamp{Time: created}}
	if err := current.planPRReview(context.Background(), opened, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || !current.changes[0].start.Equal(created) || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("opened pull request changes = %#v, want the 48-hour reminder from created_at", current.changes)
	}
}

func TestPlanRequestedChangesIgnoresALaterMaintainerReview(t *testing.T) {
	reviewAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	client := githubTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pulls/4/commits") {
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[]`)
	})
	current := &state{
		client: client, owner: "owner", repository: "repo", now: reviewAt.Add(100 * time.Hour),
		roles: map[string]string{"maintainer": "maintain", "other": "admin"},
	}
	pull := &github.PullRequest{Number: github.Ptr(4), User: &github.User{Login: github.Ptr("contributor")}}
	for _, test := range []struct {
		name       string
		laterUser  string
		laterState string
		want       int
	}{
		{name: "later approval", laterUser: "maintainer", laterState: "APPROVED"},
		{name: "later comment", laterUser: "maintainer", laterState: "COMMENTED"},
		{name: "other maintainer approval", laterUser: "other", laterState: "APPROVED", want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			current.changes = nil
			reviews := []*github.PullRequestReview{
				submittedReviewAt("maintainer", "CHANGES_REQUESTED", reviewAt),
				submittedReviewAt(test.laterUser, test.laterState, reviewAt.Add(time.Hour)),
			}
			if err := current.planRequestedChanges(context.Background(), pull, reviews, nil, nil); err != nil {
				t.Fatal(err)
			}
			if len(current.changes) != test.want {
				t.Fatalf("changes = %#v, want %d", current.changes, test.want)
			}
		})
	}
}

func TestPlanRequestedChangesCountsRebasedCommitterDate(t *testing.T) {
	reviewAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	authorDate := reviewAt.Add(-48 * time.Hour)
	committerDate := reviewAt.Add(2 * time.Hour)
	client := githubTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/pulls/4/commits") {
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"sha":"abc","author":{"login":"contributor"},"commit":{"author":{"name":"Contributor Name","date":%q},"committer":{"name":"contributor","date":%q}}}]`,
			authorDate.Format(time.RFC3339), committerDate.Format(time.RFC3339))
	})
	current := &state{
		client: client, owner: "owner", repository: "repo", now: reviewAt.Add(100 * time.Hour),
		roles: map[string]string{"maintainer": "maintain"},
	}
	pull := &github.PullRequest{Number: github.Ptr(4), User: &github.User{Login: github.Ptr("contributor")}}
	events := []*github.IssueEvent{{
		Event:     github.Ptr("review_requested"),
		Actor:     &github.User{Login: github.Ptr("contributor")},
		CreatedAt: &github.Timestamp{Time: committerDate.Add(time.Minute)},
	}}
	err := current.planRequestedChanges(context.Background(), pull, []*github.PullRequestReview{
		submittedReviewAt("maintainer", "CHANGES_REQUESTED", reviewAt),
	}, events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("changes = %#v, want no reminder after a rebased fix and review request", current.changes)
	}
}

func TestPlanMergeReviewSkipsMaintainerApprovals(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	requestedAt := start.Add(time.Hour)
	current := &state{
		now: requestedAt.Add(30 * time.Hour),
		roles: map[string]string{
			"maintainer":  "maintain",
			"contributor": "read",
			"reviewer":    "admin",
		},
	}
	pull := &github.PullRequest{Number: github.Ptr(12)}
	for _, test := range []struct {
		name     string
		approver string
		want     int
	}{
		{name: "maintainer approval", approver: "maintainer"},
		{name: "non-maintainer approval", approver: "contributor", want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			current.changes = nil
			reviews := []*github.PullRequestReview{submittedReviewAt(test.approver, "APPROVED", start)}
			events := []*github.IssueEvent{{
				Event:             github.Ptr("review_requested"),
				Actor:             &github.User{Login: github.Ptr(test.approver)},
				RequestedReviewer: &github.User{Login: github.Ptr("reviewer")},
				CreatedAt:         &github.Timestamp{Time: requestedAt},
			}}
			if err := current.planMergeReview(context.Background(), pull, reviews, events, nil); err != nil {
				t.Fatal(err)
			}
			if len(current.changes) != test.want {
				t.Fatalf("changes = %#v, want %d", current.changes, test.want)
			}
			if test.want == 0 {
				return
			}
			body := current.changes[0].body
			if strings.Contains(body, "a maintainer approved") || !strings.Contains(body, test.approver+" approved") {
				t.Fatalf("body = %s, want the approver named without calling them a maintainer", body)
			}
			if !strings.Contains(body, "target=reviewer") || strings.Contains(body, "target="+test.approver) {
				t.Fatalf("marker = %s, want target=reviewer", body)
			}
		})
	}
}

func TestMergeReviewBodyNamesApproverOnlyWhenPassed(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	named := reminderBody(mergeReviewObjective, start, 24*time.Hour, "reviewer", "contributor")
	if strings.Contains(named, "a maintainer approved") || !strings.Contains(named, "contributor approved") {
		t.Fatalf("named body = %s", named)
	}
	if !strings.Contains(named, "target=reviewer") || strings.Contains(named, "target=contributor") {
		t.Fatalf("marker = %s, want target=reviewer", named)
	}
	unnamed := reminderBody(mergeReviewObjective, start, 24*time.Hour, "reviewer", "")
	if strings.Contains(unnamed, "a maintainer approved") || strings.Contains(unnamed, "contributor approved") {
		t.Fatalf("unnamed body = %s", unnamed)
	}
}

func TestIsMaintainerTreatsNotFoundAsNonMaintainer(t *testing.T) {
	var calls int
	client := githubTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/collaborators/missing/permission"):
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
		case strings.HasSuffix(r.URL.Path, "/collaborators/broken/permission"):
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"message":"boom"}`)
		default:
			http.NotFound(w, r)
		}
	})
	current := &state{client: client, owner: "owner", repository: "repo", roles: map[string]string{}}
	maintainer, err := current.isMaintainer(context.Background(), "missing")
	if err != nil || maintainer {
		t.Fatalf("missing maintainer=%v err=%v, want false and nil", maintainer, err)
	}
	maintainer, err = current.isMaintainer(context.Background(), "missing")
	if err != nil || maintainer || calls != 1 {
		t.Fatalf("cached missing maintainer=%v err=%v calls=%d, want false, nil, 1", maintainer, err, calls)
	}
	if _, err = current.isMaintainer(context.Background(), "broken"); err == nil {
		t.Fatal("non-404 permission error was ignored")
	}
}

func TestIssueTriaged(t *testing.T) {
	current := &state{roles: map[string]string{"maintainer": "maintain"}}
	label := &github.Label{Name: "needs triage"}
	for _, test := range []struct {
		name   string
		events []*github.IssueEvent
		want   bool
	}{
		{
			name: "maintainer unlabeled needs triage",
			events: []*github.IssueEvent{{
				Event: github.Ptr("unlabeled"),
				Actor: &github.User{Login: github.Ptr("maintainer")},
				Label: label,
			}},
			want: true,
		},
		{
			name: "bot labeled needs triage",
			events: []*github.IssueEvent{{
				Event: github.Ptr("labeled"),
				Actor: &github.User{Login: github.Ptr(reminderUser)},
				Label: label,
			}},
		},
		{
			name: "maintainer unlabeled another label",
			events: []*github.IssueEvent{{
				Event: github.Ptr("unlabeled"),
				Actor: &github.User{Login: github.Ptr("maintainer")},
				Label: &github.Label{Name: "bug"},
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := current.issueTriaged(context.Background(), test.events)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("issueTriaged() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMarkerCommentSuppressesOnlyTheReminderBot(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	body := reminderBody(changesObjective, start, 48*time.Hour, "contributor", "")
	current := &state{now: start.Add(50 * time.Hour)}
	for _, test := range []struct {
		name  string
		login string
		want  int
	}{
		{name: "bot marker", login: reminderUser},
		{name: "other commenter", login: "someone", want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			current.changes = nil
			markers := remindersFrom([]*github.IssueComment{{
				User: &github.User{Login: github.Ptr(test.login)},
				Body: github.Ptr(body),
			}})
			current.planReminder(changesObjective, 42, start, "contributor", "", markers)
			if len(current.changes) != test.want {
				t.Fatalf("changes = %#v, want %d", current.changes, test.want)
			}
		})
	}
}

func TestReviewRequestResolved(t *testing.T) {
	start := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name    string
		reviews []*github.PullRequestReview
		events  []*github.IssueEvent
		want    bool
	}{
		{
			name:    "later review from target",
			reviews: []*github.PullRequestReview{submittedReviewAt("reviewer", "APPROVED", start.Add(time.Hour))},
			want:    true,
		},
		{
			name: "removed request",
			events: []*github.IssueEvent{{
				Event:             github.Ptr("review_request_removed"),
				RequestedReviewer: &github.User{Login: github.Ptr("reviewer")},
				CreatedAt:         &github.Timestamp{Time: start.Add(time.Hour)},
			}},
			want: true,
		},
		{
			name:    "review before request",
			reviews: []*github.PullRequestReview{submittedReviewAt("reviewer", "COMMENTED", start.Add(-time.Hour))},
		},
		{
			name:    "different reviewer",
			reviews: []*github.PullRequestReview{submittedReviewAt("other", "APPROVED", start.Add(time.Hour))},
		},
		{
			name:    "pending review",
			reviews: []*github.PullRequestReview{submittedReviewAt("reviewer", "PENDING", start.Add(time.Hour))},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := reviewRequestResolved("reviewer", start, test.reviews, test.events); got != test.want {
				t.Fatalf("reviewRequestResolved() = %v, want %v", got, test.want)
			}
		})
	}
}

func submittedReviewAt(login, state string, at time.Time) *github.PullRequestReview {
	return &github.PullRequestReview{
		User:        &github.User{Login: github.Ptr(login)},
		State:       github.Ptr(state),
		SubmittedAt: &github.Timestamp{Time: at},
	}
}

func githubTestClient(t *testing.T, handler http.HandlerFunc) *github.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	baseURL.Path += "/"
	base := baseURL.String()
	client, err := github.NewClient(github.WithAuthToken("token"), github.WithURLs(&base, nil))
	if err != nil {
		t.Fatal(err)
	}
	return client
}
