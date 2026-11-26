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
	client      *github.Client
	owner       string
	repository  string
	now         time.Time
	maintainers map[string]bool
	changes     []reminder
	calendar    *holidayCalendar
}

func (s *state) isMaintainer(ctx context.Context, login string) (bool, error) {
	if login == "" || login == reminderUser {
		return false, nil
	}
	if maintainer, ok := s.maintainers[login]; ok {
		return maintainer, nil
	}
	permission, _, err := s.client.Repositories.GetPermissionLevel(ctx, s.owner, s.repository, login)
	if contribution.IsNotFound(err) {
		s.maintainers[login] = false
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read repository role for @%s: %w", login, err)
	}
	role := permission.GetRoleName()
	maintainer := role == "admin" || role == "maintain"
	s.maintainers[login] = maintainer
	return maintainer, nil
}
