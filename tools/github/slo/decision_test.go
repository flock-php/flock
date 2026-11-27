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
	current := &state{now: ready.Add(time.Hour), maintainers: map[string]bool{"maintainer": true, "contributor": false}}
	draft := &github.PullRequest{Number: github.Ptr(8), Draft: github.Ptr(true), CreatedAt: &github.Timestamp{Time: created}}
	if err := current.planPRReview(context.Background(), draft, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("draft changes = %#v, want no reminder", current.changes)
	}

	events := []*github.IssueEvent{
		{Event: github.Ptr("ready_for_review"), CreatedAt: &github.Timestamp{Time: firstReady}},
		{Event: github.Ptr("convert_to_draft"), CreatedAt: &github.Timestamp{Time: created.Add(2 * time.Hour)}},
		{Event: github.Ptr("ready_for_review"), CreatedAt: &github.Timestamp{Time: ready}},
	}
	converted := &github.PullRequest{Number: github.Ptr(9), CreatedAt: &github.Timestamp{Time: created}}
	if err := current.planPRReview(context.Background(), converted, nil, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("converted draft changes = %#v, want no reminder until 48 hours after ready_for_review", current.changes)
	}

	current.now = ready.Add(50 * time.Hour)
	draftComment := []*github.PullRequestReview{submittedReviewAt("maintainer", "COMMENTED", firstReady)}
	if err := current.planPRReview(context.Background(), converted, draftComment, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || !current.changes[0].start.Equal(ready) || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("review before ready_for_review changes = %#v, want the 48-hour reminder from ready_for_review", current.changes)
	}

	current.changes = nil
	atReady := []*github.PullRequestReview{submittedReviewAt("maintainer", "COMMENTED", ready)}
	if err := current.planPRReview(context.Background(), converted, atReady, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || !current.changes[0].start.Equal(ready) {
		t.Fatalf("review at ready_for_review changes = %#v, want the reminder from ready_for_review", current.changes)
	}

	current.changes = nil
	afterReady := []*github.PullRequestReview{
		submittedReviewAt("maintainer", "COMMENTED", firstReady),
		submittedReviewAt("maintainer", "APPROVED", ready.Add(time.Minute)),
	}
	if err := current.planPRReview(context.Background(), converted, afterReady, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("review after ready_for_review changes = %#v, want no reminder", current.changes)
	}

	current.changes = nil
	contributorReview := []*github.PullRequestReview{submittedReviewAt("contributor", "COMMENTED", ready.Add(time.Minute))}
	if err := current.planPRReview(context.Background(), converted, contributorReview, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || !current.changes[0].start.Equal(ready) {
		t.Fatalf("non-maintainer review changes = %#v, want the reminder from ready_for_review", current.changes)
	}

	current.changes = nil
	current.now = created.Add(100 * time.Hour)
	opened := &github.PullRequest{Number: github.Ptr(10), CreatedAt: &github.Timestamp{Time: created}}
	if err := current.planPRReview(context.Background(), opened, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || !current.changes[0].start.Equal(created) || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("opened pull request changes = %#v, want the 48-hour reminder from created_at", current.changes)
	}
}

func TestPlanMergeReviewSkipsDrafts(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	requestedAt := start.Add(time.Hour)
	current := &state{
		now: requestedAt.Add(30 * time.Hour),
		mergeAuthorized: map[string]bool{
			"writer": false,
			"merger": true,
		},
	}
	reviews := []*github.PullRequestReview{submittedReviewAt("writer", "APPROVED", start)}
	events := []*github.IssueEvent{reviewRequested("writer", "merger", requestedAt)}
	draft := &github.PullRequest{Number: github.Ptr(8), Draft: github.Ptr(true)}
	if err := current.planMergeReview(context.Background(), draft, reviews, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 0 {
		t.Fatalf("draft changes = %#v, want no merge-review reminder", current.changes)
	}

	ready := &github.PullRequest{Number: github.Ptr(8)}
	if err := current.planMergeReview(context.Background(), ready, reviews, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || current.changes[0].target != "merger" {
		t.Fatalf("ready changes = %#v, want a merge-review reminder for the same approval and request", current.changes)
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
		maintainers: map[string]bool{"maintainer": true, "other": true},
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
		maintainers: map[string]bool{"maintainer": true},
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

func TestPlanMergeReviewSkipsMergeAuthorizedApprovals(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	requestedAt := start.Add(time.Hour)
	current := &state{
		now: requestedAt.Add(30 * time.Hour),
		mergeAuthorized: map[string]bool{
			"maintainer":  true,
			"contributor": false,
			"reviewer":    true,
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

func TestPlanMergeReviewPostsOneReminderPerTarget(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	firstRequested := start.Add(time.Hour)
	secondRequested := start.Add(2 * time.Hour)
	current := &state{
		now: secondRequested.Add(30 * time.Hour),
		mergeAuthorized: map[string]bool{
			"contributor": false,
			"other":       false,
			"reviewer":    true,
			"maintainer":  true,
		},
	}
	pull := &github.PullRequest{Number: github.Ptr(12)}
	reviews := []*github.PullRequestReview{
		submittedReviewAt("contributor", "APPROVED", start),
		submittedReviewAt("other", "APPROVED", start.Add(30*time.Minute)),
	}
	events := []*github.IssueEvent{
		reviewRequested("contributor", "reviewer", secondRequested),
		reviewRequested("other", "reviewer", firstRequested),
		reviewRequested("contributor", "reviewer", secondRequested),
		reviewRequested("contributor", "maintainer", secondRequested),
	}
	if err := current.planMergeReview(context.Background(), pull, reviews, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 2 {
		t.Fatalf("changes = %#v, want one reminder per target", current.changes)
	}
	if current.changes[0].target != "reviewer" || !current.changes[0].start.Equal(firstRequested) ||
		!strings.Contains(current.changes[0].body, "other approved") {
		t.Fatalf("reviewer reminder = %#v, want the earliest unresolved request from other", current.changes[0])
	}
	if current.changes[1].target != "maintainer" || !current.changes[1].start.Equal(secondRequested) ||
		!strings.Contains(current.changes[1].body, "contributor approved") {
		t.Fatalf("maintainer reminder = %#v, want one request from contributor", current.changes[1])
	}

	current.changes = nil
	markers := []marker{{
		objective: mergeReviewObjective,
		start:     firstRequested,
		threshold: 24 * time.Hour,
		target:    "reviewer",
	}}
	if err := current.planMergeReview(context.Background(), pull, reviews, events, markers); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 1 || current.changes[0].target != "maintainer" || !current.changes[0].start.Equal(secondRequested) {
		t.Fatalf("changes = %#v, want the earliest reviewer marker to suppress only that target", current.changes)
	}

	current.changes = nil
	resolved := append(reviews, submittedReviewAt("reviewer", "COMMENTED", firstRequested.Add(time.Minute)))
	if err := current.planMergeReview(context.Background(), pull, resolved, events, nil); err != nil {
		t.Fatal(err)
	}
	if len(current.changes) != 2 || current.changes[0].target != "maintainer" || current.changes[1].target != "reviewer" ||
		!current.changes[1].start.Equal(secondRequested) || !strings.Contains(current.changes[1].body, "contributor approved") {
		t.Fatalf("changes = %#v, want the later unresolved reviewer request after the earlier one was answered", current.changes)
	}
}

func TestPlanMergeReviewRequiresMergeAuthority(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	requestedAt := start.Add(time.Hour)
	pull := &github.PullRequest{Number: github.Ptr(12)}
	for _, test := range []struct {
		name       string
		approver   string
		target     string
		authorized map[string]bool
		wantTarget string
	}{
		{
			name:       "write reviewer is not notified",
			approver:   "contributor",
			target:     "writer",
			authorized: map[string]bool{"contributor": false, "writer": false},
		},
		{
			name:       "write approver needs merge review",
			approver:   "writer",
			target:     "merger",
			authorized: map[string]bool{"writer": false, "merger": true},
			wantTarget: "merger",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := &state{
				now:             requestedAt.Add(30 * time.Hour),
				mergeAuthorized: test.authorized,
			}
			reviews := []*github.PullRequestReview{submittedReviewAt(test.approver, "APPROVED", start)}
			events := []*github.IssueEvent{reviewRequested(test.approver, test.target, requestedAt)}
			if err := current.planMergeReview(context.Background(), pull, reviews, events, nil); err != nil {
				t.Fatal(err)
			}
			if test.wantTarget == "" {
				if len(current.changes) != 0 {
					t.Fatalf("changes = %#v, want no reminders", current.changes)
				}
				return
			}
			if len(current.changes) != 1 || current.changes[0].target != test.wantTarget {
				t.Fatalf("changes = %#v, want a reminder for %q", current.changes, test.wantTarget)
			}
		})
	}
}

func reviewRequested(actor, reviewer string, at time.Time) *github.IssueEvent {
	return &github.IssueEvent{
		Event:             github.Ptr("review_requested"),
		Actor:             &github.User{Login: github.Ptr(actor)},
		RequestedReviewer: &github.User{Login: github.Ptr(reviewer)},
		CreatedAt:         &github.Timestamp{Time: at},
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
		case strings.HasSuffix(r.URL.Path, "/collaborators/maintainer/permission"):
			io.WriteString(w, `{"role_name":"maintain"}`)
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
	current := &state{client: client, owner: "owner", repository: "repo", maintainers: map[string]bool{}}
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
	maintainer, err = current.isMaintainer(context.Background(), "maintainer")
	if err != nil || !maintainer {
		t.Fatalf("maintainer=%v err=%v, want true and nil", maintainer, err)
	}
	maintainer, err = current.isMaintainer(context.Background(), "maintainer")
	if err != nil || !maintainer || calls != 3 {
		t.Fatalf("cached maintainer=%v err=%v calls=%d, want true, nil, 3", maintainer, err, calls)
	}
}

func TestIssueTriaged(t *testing.T) {
	current := &state{maintainers: map[string]bool{"maintainer": true, "contributor": false}}
	label := &github.Label{Name: "needs triage"}
	waiting := &github.Issue{Labels: []*github.Label{label}}
	cleared := &github.Issue{}
	maintainerUnlabeled := &github.IssueEvent{
		Event: github.Ptr("unlabeled"),
		Actor: &github.User{Login: github.Ptr("maintainer")},
		Label: label,
	}
	for _, test := range []struct {
		name   string
		issue  *github.Issue
		events []*github.IssueEvent
		want   bool
	}{
		{
			name:   "maintainer unlabeled needs triage",
			issue:  cleared,
			events: []*github.IssueEvent{maintainerUnlabeled},
			want:   true,
		},
		{
			name:  "maintainer labeled needs triage",
			issue: waiting,
			events: []*github.IssueEvent{{
				Event: github.Ptr("labeled"),
				Actor: &github.User{Login: github.Ptr("maintainer")},
				Label: label,
			}},
		},
		{
			name:  "maintainer unlabeled while needs triage is present again",
			issue: waiting,
			events: []*github.IssueEvent{
				maintainerUnlabeled,
				{
					Event: github.Ptr("labeled"),
					Actor: &github.User{Login: github.Ptr("maintainer")},
					Label: label,
				},
			},
		},
		{
			name:  "bot labeled needs triage",
			issue: waiting,
			events: []*github.IssueEvent{{
				Event: github.Ptr("labeled"),
				Actor: &github.User{Login: github.Ptr(reminderUser)},
				Label: label,
			}},
		},
		{
			name:  "bot unlabeled needs triage",
			issue: cleared,
			events: []*github.IssueEvent{{
				Event: github.Ptr("unlabeled"),
				Actor: &github.User{Login: github.Ptr(reminderUser)},
				Label: label,
			}},
		},
		{
			name:  "contributor unlabeled needs triage",
			issue: cleared,
			events: []*github.IssueEvent{{
				Event: github.Ptr("unlabeled"),
				Actor: &github.User{Login: github.Ptr("contributor")},
				Label: label,
			}},
		},
		{
			name:  "never labeled",
			issue: cleared,
			want:  true,
		},
		{
			name:  "maintainer unlabeled another label",
			issue: cleared,
			events: []*github.IssueEvent{{
				Event: github.Ptr("unlabeled"),
				Actor: &github.User{Login: github.Ptr("maintainer")},
				Label: &github.Label{Name: "bug"},
			}},
			want: true,
		},
		{
			name:  "contributor removed needs triage after a maintainer",
			issue: cleared,
			events: []*github.IssueEvent{
				{
					Event:     github.Ptr("unlabeled"),
					Actor:     &github.User{Login: github.Ptr("maintainer")},
					Label:     label,
					CreatedAt: &github.Timestamp{Time: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)},
				},
				{
					Event:     github.Ptr("unlabeled"),
					Actor:     &github.User{Login: github.Ptr("contributor")},
					Label:     label,
					CreatedAt: &github.Timestamp{Time: time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)},
				},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := current.issueTriaged(context.Background(), test.issue, test.events)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("issueTriaged() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPlanIssueTriageSkipsCommentsAfterTriage(t *testing.T) {
	client := githubTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/issues/1/events") {
			http.Error(w, `{"message":"comments must not be read for triaged issues"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"event":"unlabeled","actor":{"login":"maintainer"},"label":{"name":"needs triage"}}]`)
	})
	current := &state{
		client: client, owner: "owner", repository: "repo",
		maintainers: map[string]bool{"maintainer": true},
	}
	issue := &github.Issue{Number: github.Ptr(1)}

	if err := current.planIssueTriage(context.Background(), issue); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPostsRemainingCommentsAfterFailure(t *testing.T) {
	var posted []string
	client := githubTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/comments") {
			http.Error(w, `{"message":"unexpected"}`, http.StatusNotFound)
			return
		}
		posted = append(posted, r.URL.Path)
		if strings.Contains(r.URL.Path, "/issues/1/comments") {
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":2,"body":"second"}`)
	})
	current := &state{
		client: client, owner: "owner", repository: "repo",
		changes: []reminder{
			{number: 1, body: "first"},
			{number: 2, body: "second"},
		},
	}

	err := current.apply(context.Background())
	if err == nil || !strings.Contains(err.Error(), "create SLO reminder on #1") || strings.Contains(err.Error(), "#2") {
		t.Fatalf("apply() error = %v, want only the first comment failure", err)
	}
	if len(posted) != 2 || !strings.HasSuffix(posted[1], "/issues/2/comments") {
		t.Fatalf("posted = %#v, want the second comment after the first failure", posted)
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
