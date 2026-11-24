package slo

import "time"

const (
	issueTriageObjective = "issue-triage"
	prReviewObjective    = "pr-review"
	mergeReviewObjective = "merge-review"
	changesObjective     = "changes-requested"
)

// Objective is a waiting state and the thresholds at which it sends reminders.
type Objective struct {
	Name       string
	Thresholds []time.Duration
}

// Objectives defines the initial service targets.
var Objectives = []Objective{
	{
		Name: issueTriageObjective,
		Thresholds: []time.Duration{
			24 * time.Hour,
			72 * time.Hour,
		},
	},
	{
		Name: prReviewObjective,
		Thresholds: []time.Duration{
			48 * time.Hour,
			96 * time.Hour,
		},
	},
	{
		Name: mergeReviewObjective,
		Thresholds: []time.Duration{
			24 * time.Hour,
			72 * time.Hour,
		},
	},
	{
		Name: changesObjective,
		Thresholds: []time.Duration{
			48 * time.Hour,
			96 * time.Hour,
		},
	},
}

func thresholdsFor(name string) []time.Duration {
	for _, objective := range Objectives {
		if objective.Name == name {
			return objective.Thresholds
		}
	}
	return nil
}
