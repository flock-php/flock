package merge_gate

import (
	"errors"
	"reflect"
	"testing"
)

const ref = "refs/heads/gh-readonly-queue/main/pr-123-ce2a51d86b2e1be42ffd4c0d5a020fd0d7270588"

func TestRunAllowsAdminsAndMaintainers(t *testing.T) {
	for _, role := range []string{"admin", "maintain"} {
		t.Run(role, func(t *testing.T) {
			api := &fakeService{role: role}
			if err := Execute(Dependencies{MergeGroups: api, PullRequests: api, Repositories: api}, "flock-php", "flock", Event{HeadRef: ref, HeadSHA: "abc", RunID: 7}); err != nil {
				t.Fatal(err)
			}
			if api.commented || api.cancelled {
				t.Fatalf("commented = %t, cancelled = %t", api.commented, api.cancelled)
			}
		})
	}
}

func TestRunRejectsOtherRoles(t *testing.T) {
	for _, role := range []string{"write", "triage", "read"} {
		t.Run(role, func(t *testing.T) {
			api := &fakeService{role: role}
			err := Execute(Dependencies{MergeGroups: api, PullRequests: api, Repositories: api}, "flock-php", "flock", Event{HeadRef: ref, HeadSHA: "abc", RunID: 7})
			if !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("err = %v, want ErrNotAllowed", err)
			}
			if !api.commented || api.commentedOn != 123 {
				t.Fatalf("commented = %t on #%d, want #123", api.commented, api.commentedOn)
			}
			if !api.cancelled || api.cancelSHA != "abc" || api.cancelExcept != 7 {
				t.Fatalf("cancel = %t sha=%q except=%d", api.cancelled, api.cancelSHA, api.cancelExcept)
			}
			want := []string{"pull-request:enqueuer", "repository:role", "pull-request:comment", "merge-group:cancel"}
			if !reflect.DeepEqual(api.calls, want) {
				t.Fatalf("calls = %v, want %v", api.calls, want)
			}
		})
	}
}

func TestRunStopsOnServiceErrors(t *testing.T) {
	tests := []struct {
		name string
		api  *fakeService
		want []string
	}{
		{name: "enqueuer", api: &fakeService{enqueuerErr: errors.New("enqueuer")}, want: []string{"pull-request:enqueuer"}},
		{name: "role", api: &fakeService{roleErr: errors.New("role")}, want: []string{"pull-request:enqueuer", "repository:role"}},
		{name: "comment", api: &fakeService{commentErr: errors.New("comment")}, want: []string{"pull-request:enqueuer", "repository:role", "pull-request:comment"}},
		{name: "cancel", api: &fakeService{cancelErr: errors.New("cancel")}, want: []string{"pull-request:enqueuer", "repository:role", "pull-request:comment", "merge-group:cancel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Execute(Dependencies{MergeGroups: tt.api, PullRequests: tt.api, Repositories: tt.api}, "flock-php", "flock", Event{HeadRef: ref, HeadSHA: "abc", RunID: 7}); err == nil {
				t.Fatal("Execute() returned nil error")
			}
			if !reflect.DeepEqual(tt.api.calls, tt.want) {
				t.Fatalf("calls = %v, want %v", tt.api.calls, tt.want)
			}
		})
	}
}

func TestRunStopsBeforeSideEffectsForInvalidQueueRef(t *testing.T) {
	api := &fakeService{}
	if err := Execute(Dependencies{MergeGroups: api, PullRequests: api, Repositories: api}, "flock-php", "flock", Event{HeadRef: "refs/heads/main", HeadSHA: "abc", RunID: 7}); err == nil {
		t.Fatal("Execute() returned nil error")
	}
	if len(api.calls) != 0 || api.commented || api.cancelled {
		t.Fatalf("calls = %v, commented = %t, cancelled = %t", api.calls, api.commented, api.cancelled)
	}
}

func TestEvaluateAuthorization(t *testing.T) {
	for _, tt := range []struct {
		role string
		want authorizationState
	}{
		{role: "admin", want: allowMerge},
		{role: "maintain", want: allowMerge},
		{role: "write", want: rejectMerge},
	} {
		if got := evaluateAuthorization(tt.role); got != tt.want {
			t.Fatalf("evaluateAuthorization(%q) = %d, want %d", tt.role, got, tt.want)
		}
	}
}

func TestAuthorizationApplyRejectsUnknownState(t *testing.T) {
	err := (authorizationTransition{state: authorizationState(99)}).apply(Dependencies{}, "flock-php", "flock")
	if err == nil {
		t.Fatal("apply() returned nil error")
	}
}

func TestPullRequestNumber(t *testing.T) {
	number, err := PullRequestNumber(ref)
	if err != nil || number != 123 {
		t.Fatalf("number = %d, err = %v", number, err)
	}
	if _, err := PullRequestNumber("refs/heads/main"); err == nil {
		t.Fatal("want an error for a non-queue ref")
	}
}

type fakeService struct {
	role         string
	commented    bool
	commentedOn  int
	cancelled    bool
	cancelSHA    string
	cancelExcept int64
	calls        []string
	enqueuerErr  error
	roleErr      error
	commentErr   error
	cancelErr    error
}

func (f *fakeService) GetMergeQueueEnqueuer(string, string, int) (string, error) {
	f.calls = append(f.calls, "pull-request:enqueuer")
	return "someone", f.enqueuerErr
}
func (f *fakeService) GetUserRole(string, string, string) (string, error) {
	f.calls = append(f.calls, "repository:role")
	return f.role, f.roleErr
}
func (f *fakeService) CreateComment(_ string, _ string, number int, _ string) error {
	f.calls = append(f.calls, "pull-request:comment")
	f.commented, f.commentedOn = true, number
	return f.commentErr
}
func (f *fakeService) CancelOtherRuns(_ string, _ string, sha string, except int64) error {
	f.calls = append(f.calls, "merge-group:cancel")
	f.cancelled, f.cancelSHA, f.cancelExcept = true, sha, except
	return f.cancelErr
}
