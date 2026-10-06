package store

import (
	"testing"
	"time"
)

// 午夜不是默认值哨兵：统计必须在 00:00 而不是 04:00 开始新复习日。
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
