package slo

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
)

type marker struct {
	objective string
	start     time.Time
	threshold time.Duration
	target    string
}

var markerPattern = regexp.MustCompile(`(?m)^<!-- slo:reminder objective=([a-z-]+) start=([^ ]+) threshold=([^ ]+) target=([^ ]*) -->$`)

func (s *state) planReminder(objective string, number int, start time.Time, target string, markers []marker) {
	if start.IsZero() || !start.Before(s.now) {
		return
	}
	for _, threshold := range thresholdsFor(objective) {
		if s.now.Sub(start) < threshold || reminded(markers, objective, start, threshold, target) {
			continue
		}
		s.changes = append(s.changes, reminder{
			objective: objective,
			number:    number,
			start:     start,
			threshold: threshold,
			target:    target,
			body:      reminderBody(objective, start, threshold, target),
		})
		return
	}
}

func (s *state) apply(ctx context.Context) error {
	for _, change := range s.changes {
		if _, _, err := s.client.Issues.CreateComment(ctx, s.owner, s.repository, change.number, github.IssueCommentRequest{Body: change.body}); err != nil {
			return fmt.Errorf("create SLO reminder on #%d: %w", change.number, err)
		}
	}
	return nil
}

func remindersFrom(comments []*github.IssueComment) []marker {
	var markers []marker
	for _, comment := range comments {
		if comment.GetUser().GetLogin() != reminderUser {
			continue
		}
		for _, match := range markerPattern.FindAllStringSubmatch(comment.GetBody(), -1) {
			start, startErr := time.Parse(time.RFC3339, match[2])
			threshold, thresholdErr := time.ParseDuration(match[3])
			if startErr == nil && thresholdErr == nil {
				markers = append(markers, marker{objective: match[1], start: start, threshold: threshold, target: match[4]})
			}
		}
	}
	return markers
}

func reminded(markers []marker, objective string, start time.Time, threshold time.Duration, target string) bool {
	return slices.ContainsFunc(markers, func(marker marker) bool {
		return marker.objective == objective && marker.start.Equal(start) && marker.threshold == threshold && marker.target == strings.ToLower(target)
	})
}

func reminderBody(objective string, start time.Time, threshold time.Duration, target string) string {
	var text string
	switch objective {
	case issueTriageObjective:
		text = "A contributor is waiting for triage on this issue. Could a maintainer please update the `needs triage` status when they start reviewing it?"
	case prReviewObjective:
		text = "A contributor is waiting for a maintainer review on this pull request. Could a maintainer please review it or leave feedback when possible?"
	case mergeReviewObjective:
		text = fmt.Sprintf("@%s, a maintainer approved this pull request and requested your merge-maintainer review. Could you please review or approve it when possible?", target)
	case changesObjective:
		text = fmt.Sprintf("@%s, this pull request has requested changes. When you are ready, please push the updates and request another review.", target)
	}
	return fmt.Sprintf("%s\n\n<!-- slo:reminder objective=%s start=%s threshold=%s target=%s -->",
		text, objective, start.UTC().Format(time.RFC3339), threshold, strings.ToLower(target))
}
