package merge_gate

import (
	"errors"
	"testing"
)

const ref = "refs/heads/gh-readonly-queue/main/pr-123-ce2a51d86b2e1be42ffd4c0d5a020fd0d7270588"

func TestRunAllowsAdminsAndMaintainers(t *testing.T) {
	for _, role := range []string{"admin", "maintain"} {
		t.Run(role, func(t *testing.T) {
			api := &fakeService{role: role}
			if err := Run(api, "flock-php", "flock", Event{HeadRef: ref, HeadSHA: "abc", RunID: 7}); err != nil {
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
			err := Run(api, "flock-php", "flock", Event{HeadRef: ref, HeadSHA: "abc", RunID: 7})
			if !errors.Is(err, ErrNotAllowed) {
				t.Fatalf("err = %v, want ErrNotAllowed", err)
			}
			if !api.commented || api.commentedOn != 123 {
				t.Fatalf("commented = %t on #%d, want #123", api.commented, api.commentedOn)
			}
			if !api.cancelled || api.cancelSHA != "abc" || api.cancelExcept != 7 {
				t.Fatalf("cancel = %t sha=%q except=%d", api.cancelled, api.cancelSHA, api.cancelExcept)
			}
		})
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
}

func (f *fakeService) MergeQueueEnqueuer(string, string, int) (string, error) { return "someone", nil }
func (f *fakeService) RoleName(string, string, string) (string, error)        { return f.role, nil }
func (f *fakeService) Comment(_ string, _ string, number int, _ string) error {
	f.commented, f.commentedOn = true, number
	return nil
}
func (f *fakeService) CancelMergeGroupRuns(_ string, _ string, sha string, except int64) error {
	f.cancelled, f.cancelSHA, f.cancelExcept = true, sha, except
	return nil
}
