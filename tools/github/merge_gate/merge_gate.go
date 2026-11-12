// Package merge_gate lets only repository admins and maintainers merge through the merge queue.
package merge_gate

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

var queueRef = regexp.MustCompile(`/pr-(\d+)-[0-9a-f]+$`)

// ErrNotAllowed means the pull request was queued by someone who can't merge.
var ErrNotAllowed = errors.New("queued by someone without the admin or maintain role")

type MergeQueuePullRequestService interface {
	GetMergeQueueEnqueuer(owner, repository string, number int) (string, error)
	CreateComment(owner, repository string, number int, body string) error
}

type RepositoryRoleReader interface {
	GetUserRole(owner, repository, username string) (string, error)
}

type MergeGroupRunCanceler interface {
	CancelOtherRuns(owner, repository, headSHA string, except int64) error
}

type Dependencies struct {
	MergeGroups  MergeGroupRunCanceler
	PullRequests MergeQueuePullRequestService
	Repositories RepositoryRoleReader
}

type Event struct {
	HeadRef string
	HeadSHA string
	RunID   int64
}

type authorizationState uint8

const (
	allowMerge authorizationState = iota
	rejectMerge
)

type authorizationTransition struct {
	state    authorizationState
	number   int
	enqueuer string
	role     string
	headSHA  string
	runID    int64
}

func Execute(dependencies Dependencies, owner, repository string, event Event) error {
	number, err := PullRequestNumber(event.HeadRef)
	if err != nil {
		return err
	}
	enqueuer, err := dependencies.PullRequests.GetMergeQueueEnqueuer(owner, repository, number)
	if err != nil {
		return err
	}
	role, err := dependencies.Repositories.GetUserRole(owner, repository, enqueuer)
	if err != nil {
		return err
	}
	return authorizationTransition{
		state:    evaluateAuthorization(role),
		number:   number,
		enqueuer: enqueuer,
		role:     role,
		headSHA:  event.HeadSHA,
		runID:    event.RunID,
	}.apply(dependencies, owner, repository)
}

func evaluateAuthorization(role string) authorizationState {
	if role == "admin" || role == "maintain" {
		return allowMerge
	}
	return rejectMerge
}

func (t authorizationTransition) apply(dependencies Dependencies, owner, repository string) error {
	switch t.state {
	case allowMerge:
		return nil
	case rejectMerge:
		body := fmt.Sprintf("Only a repository admin or maintainer can merge, so this was taken out of the merge queue (queued by @%s). A maintainer will merge it.", t.enqueuer)
		if err := dependencies.PullRequests.CreateComment(owner, repository, t.number, body); err != nil {
			return err
		}
		if err := dependencies.MergeGroups.CancelOtherRuns(owner, repository, t.headSHA, t.runID); err != nil {
			return err
		}
		return fmt.Errorf("%w: @%s has the %q role", ErrNotAllowed, t.enqueuer, t.role)
	default:
		return fmt.Errorf("unknown merge authorization state %d", t.state)
	}
}

// PullRequestNumber reads the pull request number from a merge queue ref such as
// refs/heads/gh-readonly-queue/main/pr-123-<sha>.
func PullRequestNumber(headRef string) (int, error) {
	match := queueRef.FindStringSubmatch(headRef)
	if match == nil {
		return 0, fmt.Errorf("not a merge queue ref: %q", headRef)
	}
	return strconv.Atoi(match[1])
}
