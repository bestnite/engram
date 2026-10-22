package store

import (
	"testing"
	"time"
)

// Midnight is not the default sentinel: statistics must start a new review day at 00:00, not 04:00.
func TestMidnightCutoffIsCalendarDay(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 10, 2, 0, 30, 0, 0, loc)
	if got := ReviewDayString(now, loc, 0); got != "2026-10-02" {
		t.Fatalf("midnight review day = %s, want 2026-10-02", got)
	}
	if got := ReviewDayString(now, loc, 4); got != "2026-10-01" {
		t.Fatalf("default review day = %s, want 2026-10-01", got)
	}
}
