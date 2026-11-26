package slo

import (
	"fmt"
	"time"

	"github.com/rickar/cal/v2"
	"github.com/rickar/cal/v2/cz"
	"github.com/rickar/cal/v2/us"
)

const holidayLocation = "America/Chicago"

type holidayCalendar struct {
	location *time.Location
	holidays *cal.Calendar
}

func newHolidayCalendar() (*holidayCalendar, error) {
	location, err := time.LoadLocation(holidayLocation)
	if err != nil {
		return nil, fmt.Errorf("load SLO holiday time zone: %w", err)
	}

	holidays := &cal.Calendar{Cacheable: true}
	holidays.AddHoliday(us.Holidays...)
	holidays.AddHoliday(cz.Holidays...)

	return &holidayCalendar{
		location: location,
		holidays: holidays,
	}, nil
}

func (c *holidayCalendar) elapsed(start, end time.Time) time.Duration {
	if !start.Before(end) {
		return 0
	}

	start = start.In(c.location)
	end = end.In(c.location)
	elapsed := time.Duration(0)
	for day := dayStart(start); day.Before(end); day = day.AddDate(0, 0, 1) {
		nextDay := day.AddDate(0, 0, 1)
		intervalStart := maxTime(start, day)
		intervalEnd := minTime(end, nextDay)
		actual, observed, _ := c.holidays.IsHoliday(day)
		if !actual && !observed {
			elapsed += intervalEnd.Sub(intervalStart)
		}
	}
	return elapsed
}

func dayStart(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}
