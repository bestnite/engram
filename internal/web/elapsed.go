package web

import (
	"math"
	"strconv"

	"git.nite07.com/nite/engram/internal/i18n"
)

// elapsedLabel renders "how long has been spent so far", with the unit following the magnitude.
//
// Accumulated durations have no upper bound — a few thousand reviews is hours — so writing
// everything in milliseconds would put numbers like "5400000 ms" on the page that nobody can read. The rules:
//
//	< 1 s    → integer milliseconds
//	< 60 s   → seconds (one decimal)
//	< 60 min → minutes (one decimal)
//	>= 1 h   → "N hours M min" (write only "N hours" when M is 0)
//
// Rounding can push a number past a unit boundary (59.96 s → 60.0 s); in that case step up one level — otherwise you get
// reads that skip a unit, like "60 s" or "60 min". Do not merge this with intervalLabel: that one renders "how much longer
// to wait" (including the learning step's "<1 minute"), and both the vocabulary and the rounding rules differ.
func elapsedLabel(loc *i18n.Localizer, ms int64) string {
	if ms < 0 {
		ms = 0
	}
	if ms < 1000 {
		return loc.Tf("stats.duration.milliseconds", map[string]any{"value": ms})
	}
	if secs := round1(float64(ms) / 1000); secs < 60 {
		return loc.Tf("stats.duration.seconds", map[string]any{"value": decimalText(secs)})
	}
	if mins := round1(float64(ms) / 60000); mins < 60 {
		return loc.Tf("stats.duration.minutes", map[string]any{"value": decimalText(mins)})
	}
	total := int(round1(float64(ms) / 60000))
	hours, mins := total/60, total%60
	if mins == 0 {
		return loc.Tf("stats.duration.hours", map[string]any{"hours": hours})
	}
	return loc.Tf("stats.duration.hours_minutes", map[string]any{"hours": hours, "minutes": mins})
}

// round1 rounds to one decimal place.
func round1(v float64) float64 { return math.Round(v*10) / 10 }

// decimalText renders a "one decimal" value as text, dropping .0 for whole values — in the copy
// "8 s" reads cleaner than "8.0 s", while non-whole values like 8.4 s keep the decimal.
func decimalText(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
