// Package slo monitors contributor-facing service level objectives.
package slo

import (
	"context"
	"time"

	"github.com/google/go-github/v93/github"
)

// Run collects every open outside-contributor issue and pull request, plans
// any newly crossed SLO threshold, and then posts its planned comments.
func Run(ctx context.Context, client *github.Client, owner, repository string) error {
	calendar, err := newHolidayCalendar()
	if err != nil {
		return err
	}
	current := &state{
		client:      client,
		owner:       owner,
		repository:  repository,
		now:         time.Now().UTC(),
		maintainers: map[string]bool{},
		calendar:    calendar,
	}
	if err := current.collect(ctx); err != nil {
		return err
	}
	return current.apply(ctx)
}

func (s *state) collect(ctx context.Context) error {
	if err := s.collectIssues(ctx); err != nil {
		return err
	}
	return s.collectPullRequests(ctx)
}
