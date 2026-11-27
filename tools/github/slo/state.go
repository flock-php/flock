package slo

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v93/github"

	"flock/tools/github/contribution"
)

const reminderUser = "github-actions[bot]"

type reminder struct {
	objective string
	number    int
	start     time.Time
	threshold time.Duration
	target    string
	body      string
}

type state struct {
	client          *github.Client
	owner           string
	repository      string
	now             time.Time
	maintainers     map[string]bool
	mergeAuthorized map[string]bool
	changes         []reminder
	calendar        *holidayCalendar
}

func (s *state) isMaintainer(ctx context.Context, login string) (bool, error) {
	return s.hasRole(ctx, login, s.maintainers, contribution.IsMaintainerRole)
}

func (s *state) canMerge(ctx context.Context, login string) (bool, error) {
	return s.hasRole(ctx, login, s.mergeAuthorized, contribution.CanMergeRole)
}

func (s *state) hasRole(ctx context.Context, login string, roles map[string]bool, matches func(string) bool) (bool, error) {
	if login == "" || login == reminderUser {
		return false, nil
	}
	if matched, ok := roles[login]; ok {
		return matched, nil
	}
	permission, _, err := s.client.Repositories.GetPermissionLevel(ctx, s.owner, s.repository, login)
	if contribution.IsNotFound(err) {
		roles[login] = false
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read repository role for @%s: %w", login, err)
	}
	matched := matches(permission.GetRoleName())
	roles[login] = matched
	return matched, nil
}
