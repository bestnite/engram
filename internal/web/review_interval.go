package web

import (
	"math"
	"time"

	"git.nite07.com/nite/engram/internal/i18n"
)

// intervalLabel renders "how long until the next due" as localized text (Anki-style "10 minutes", "3 days").
//
// Learning steps are counted in minutes (default preset `1m,10m`), so this must be able to express sub-day waits: that is exactly
// the visible form of "the card comes back a few minutes after you answer it".
func intervalLabel(loc *i18n.Localizer, wait time.Duration) string {
	if wait <= 0 {
		return loc.T("review.interval.under_minute")
	}
	minutes := wait.Minutes()
	var unit string
	var count int
	switch {
	case minutes < 1:
		return loc.T("review.interval.under_minute")
	case minutes < 60:
		unit, count = "minutes", int(math.Round(minutes))
	case minutes < 24*60:
		unit, count = "hours", int(math.Round(minutes/60))
	case minutes < 30*24*60:
		unit, count = "days", int(math.Round(minutes/(24*60)))
	case minutes < 365*24*60:
		unit, count = "months", int(math.Round(minutes/(30*24*60)))
	default:
		unit, count = "years", int(math.Round(minutes/(365*24*60)))
	}
	// Rounding can push the number past the unit boundary (59.6 min → "60 minutes"); stepping up one unit reads more naturally.
	switch {
	case unit == "minutes" && count >= 60:
		unit, count = "hours", 1
	case unit == "hours" && count >= 24:
		unit, count = "days", 1
	case unit == "days" && count >= 30:
		unit, count = "months", 1
	case unit == "months" && count >= 12:
		unit, count = "years", 1
	}
	if count < 1 {
		count = 1
	}
	return loc.Tf("review.interval."+unit, map[string]any{"count": count})
}
