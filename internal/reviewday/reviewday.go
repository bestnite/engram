// Package reviewday holds the single definition of a user's review day: which timezone a user's clock reads in, at which
// local hour one review day ends and the next begins, and how a review day is written as a YYYY-MM-DD string.
//
// Every reader and writer of review days goes through this package: grading writes reviews.review_day, burying pushes a card
// to the start of the next review day, and the queue budgets, statistics, reminders and digests read "today". Two
// implementations of the boundary would let one side see a different "today" than the other, so there is exactly one.
package reviewday

import "time"

// DefaultCutoffHour is the local hour a review day starts at when the user has not chosen one: the day changes at 04:00.
const DefaultCutoffHour = 4

// dayLayout is the review_day string format stored in reviews.review_day.
const dayLayout = "2006-01-02"

// NormalizeCutoff returns hour when it is a valid hour of day (0-23) and DefaultCutoffHour otherwise. 0 is a legal cutoff
// (midnight), so it is never treated as "unset"; an unset cutoff is a nil pointer and goes through ResolveCutoff.
func NormalizeCutoff(hour int) int {
	if hour < 0 || hour > 23 {
		return DefaultCutoffHour
	}
	return hour
}

// ResolveCutoff turns the nullable users.day_cutoff_hour setting into the hour actually used: nil means DefaultCutoffHour.
func ResolveCutoff(hour *int) int {
	if hour == nil {
		return DefaultCutoffHour
	}
	return NormalizeCutoff(*hour)
}

// Location resolves an IANA timezone name. An empty or unknown name falls back to UTC, so dirty data in users.timezone
// can never block a review, a page or a mail: the review day is still computable, only in UTC.
func Location(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Start returns the instant the review day containing now began: the local date of (now - cutoff hours) at the cutoff hour,
// in UTC. A nil loc means UTC; cutoffHour is normalised first.
func Start(now time.Time, loc *time.Location, cutoffHour int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	cutoffHour = NormalizeCutoff(cutoffHour)
	day := now.In(loc).Add(-time.Duration(cutoffHour) * time.Hour)
	return time.Date(day.Year(), day.Month(), day.Day(), cutoffHour, 0, 0, 0, loc).UTC()
}

// NextStart returns the instant the review day after the one containing now begins, in UTC. It adds one calendar day in
// loc rather than 24 hours, so a daylight-saving change still lands on the cutoff hour.
func NextStart(now time.Time, loc *time.Location, cutoffHour int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return Start(now, loc, cutoffHour).In(loc).AddDate(0, 0, 1).UTC()
}

// Day returns the review day containing now as YYYY-MM-DD, the value written to and compared with reviews.review_day.
func Day(now time.Time, loc *time.Location, cutoffHour int) string {
	if loc == nil {
		loc = time.UTC
	}
	return Start(now, loc, cutoffHour).In(loc).Format(dayLayout)
}

// Shift moves a YYYY-MM-DD review day by delta days. A string that does not parse is returned unchanged.
func Shift(day string, delta int) string {
	t, err := time.Parse(dayLayout, day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, delta).Format(dayLayout)
}
