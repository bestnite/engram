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
	y, m, d := now.In(loc).Add(-time.Duration(cutoffHour) * time.Hour).Date()
	return localDayStart(y, m, d, cutoffHour, loc)
}

// NextStart returns the instant the review day after the one containing now begins, in UTC. It advances one local
// calendar day rather than 24 hours, so a daylight-saving change still lands on the cutoff hour.
func NextStart(now time.Time, loc *time.Location, cutoffHour int) time.Time {
	return AddDays(now, loc, cutoffHour, 1)
}

// AddDays returns the instant the review day n local calendar days after the one containing base begins, in UTC. Days
// are counted on the local calendar, so a daylight-saving change keeps the cutoff hour instead of drifting by an hour.
func AddDays(base time.Time, loc *time.Location, cutoffHour, n int) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	cutoffHour = NormalizeCutoff(cutoffHour)
	y, m, d := Start(base, loc, cutoffHour).In(loc).Date()
	return localDayStart(y, m, d+n, cutoffHour, loc)
}

// localDayStart returns the instant the review day on local date y-m-d begins, in UTC. It is the earliest instant whose
// local clock reads the cutoff hour, and the transition instant when a daylight-saving change skips that hour.
//
// time.Date alone is not enough for those two days: it resolves a skipped hour backwards, so the day would begin up to
// an hour before the cutoff, and its choice between two repeated occurrences of the hour is unspecified. Both break the
// review day's length and every boundary derived from it, so the instant is resolved from the zone offsets in force
// around that date instead.
func localDayStart(y int, m time.Month, d, cutoffHour int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	// The wall clock at the cutoff, written as a UTC instant so adding an offset cannot itself be ambiguous.
	wall := time.Date(y, m, d, cutoffHour, 0, 0, 0, time.UTC)
	offsets := make(map[int]struct{}, 2)
	for _, probe := range []time.Time{wall.Add(-48 * time.Hour), wall.Add(48 * time.Hour)} {
		_, offset := probe.In(loc).Zone()
		offsets[offset] = struct{}{}
	}
	var first time.Time
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		local := candidate.In(loc)
		if local.Year() != y || local.Month() != m || local.Day() != d {
			continue
		}
		// With the offset in force at the candidate the clock reads the cutoff. A skipped hour reads up to the
		// shift past it instead, which is the transition instant.
		if local.Hour() < cutoffHour || local.Hour() > cutoffHour+2 {
			continue
		}
		if first.IsZero() || candidate.Before(first) {
			first = candidate
		}
	}
	if first.IsZero() {
		// No daylight-saving change near this date: time.Date is exact.
		return time.Date(y, m, d, cutoffHour, 0, 0, 0, loc).UTC()
	}
	return first.UTC()
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
