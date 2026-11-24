package slo

import (
	"testing"
	"time"
)

func TestPlanReminderUsesTheLowestUnsentThreshold(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	current := &state{now: start.Add(100 * time.Hour)}

	current.planReminder(changesObjective, 42, start, "contributor", "", nil)

	if len(current.changes) != 1 || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("changes = %#v, want the 48-hour reminder", current.changes)
	}

	current.changes = nil
	current.planReminder(changesObjective, 42, start, "contributor", "", []marker{{
		objective: changesObjective, start: start, threshold: 48 * time.Hour, target: "contributor",
	}})
	if len(current.changes) != 1 || current.changes[0].threshold != 96*time.Hour {
		t.Fatalf("changes = %#v, want the 96-hour reminder", current.changes)
	}
}

func TestPlanReminderDoesNotRepeatAndResetsForNewWaitingState(t *testing.T) {
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	current := &state{now: start.Add(100 * time.Hour)}
	markers := []marker{
		{objective: changesObjective, start: start, threshold: 48 * time.Hour, target: "contributor"},
		{objective: changesObjective, start: start, threshold: 96 * time.Hour, target: "contributor"},
	}

	current.planReminder(changesObjective, 42, start, "contributor", "", markers)
	if len(current.changes) != 0 {
		t.Fatalf("changes = %#v, want no duplicate reminder", current.changes)
	}

	nextStart := start.Add(10 * time.Hour)
	current.planReminder(changesObjective, 42, nextStart, "contributor", "", markers)
	if len(current.changes) != 1 || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("changes = %#v, want a new 48-hour reminder", current.changes)
	}
}
