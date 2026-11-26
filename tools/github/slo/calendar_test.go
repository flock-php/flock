package slo

import (
	"testing"
	"time"
)

func TestHolidayCalendarIncludesUSAndCzechHolidays(t *testing.T) {
	calendar, err := newHolidayCalendar()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name     string
		date     time.Time
		actual   bool
		observed bool
	}{
		{
			name:     "US observed Independence Day",
			date:     chicagoTime(2026, time.July, 3, 12),
			observed: true,
		},
		{
			name:     "Czech Saints Cyril and Methodius Day",
			date:     chicagoTime(2026, time.July, 5, 12),
			actual:   true,
			observed: true,
		},
		{
			name: "ordinary day",
			date: chicagoTime(2026, time.July, 7, 12),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, observed, _ := calendar.holidays.IsHoliday(test.date)
			if actual != test.actual || observed != test.observed {
				t.Fatalf("IsHoliday(%s) = actual %v, observed %v, want actual %v, observed %v",
					test.date, actual, observed, test.actual, test.observed)
			}
		})
	}
}

func TestHolidayCalendarElapsedExcludesFullAndPartialHolidays(t *testing.T) {
	calendar, err := newHolidayCalendar()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		start time.Time
		end   time.Time
		want  time.Duration
	}{
		{
			name:  "full Thanksgiving Day",
			start: chicagoTime(2026, time.November, 25, 12),
			end:   chicagoTime(2026, time.November, 27, 12),
			want:  24 * time.Hour,
		},
		{
			name:  "partial Thanksgiving Day",
			start: chicagoTime(2026, time.November, 26, 12),
			end:   chicagoTime(2026, time.November, 27, 12),
			want:  12 * time.Hour,
		},
		{
			name:  "back-to-back Christmas Eve and Christmas Day",
			start: chicagoTime(2026, time.December, 23, 12),
			end:   chicagoTime(2026, time.December, 26, 0),
			want:  12 * time.Hour,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := calendar.elapsed(test.start, test.end); got != test.want {
				t.Fatalf("elapsed(%s, %s) = %s, want %s", test.start, test.end, got, test.want)
			}
		})
	}
}

func TestPlanReminderWaitsForNonHolidayTime(t *testing.T) {
	calendar, err := newHolidayCalendar()
	if err != nil {
		t.Fatal(err)
	}

	start := chicagoTime(2026, time.July, 2, 12)
	current := &state{
		now:      chicagoTime(2026, time.July, 5, 12),
		calendar: calendar,
	}
	current.planReminder(changesObjective, 42, start, "contributor", "", nil)
	if len(current.changes) != 0 {
		t.Fatalf("changes = %#v, want no reminder during the paused holiday time", current.changes)
	}

	current.now = chicagoTime(2026, time.July, 8, 12)
	if got := current.elapsed(start); got != 48*time.Hour {
		t.Fatalf("elapsed = %s, want %s", got, 48*time.Hour)
	}
	current.planReminder(changesObjective, 42, start, "contributor", "", nil)
	if len(current.changes) != 1 || current.changes[0].threshold != 48*time.Hour {
		t.Fatalf("changes = %#v, want the delayed 48-hour reminder", current.changes)
	}

	current.changes = nil
	current.now = chicagoTime(2026, time.July, 10, 12)
	current.planReminder(changesObjective, 42, start, "contributor", "", []marker{{
		objective: changesObjective,
		start:     start,
		threshold: 48 * time.Hour,
		target:    "contributor",
	}})
	if len(current.changes) != 1 || current.changes[0].threshold != 96*time.Hour {
		t.Fatalf("changes = %#v, want the delayed 96-hour reminder", current.changes)
	}
}

func chicagoTime(year int, month time.Month, day, hour int) time.Time {
	location, err := time.LoadLocation(holidayLocation)
	if err != nil {
		panic(err)
	}
	return time.Date(year, month, day, hour, 0, 0, 0, location)
}
