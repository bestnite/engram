package reviewday

import (
	"testing"
	"time"
)

func TestResolveCutoff(t *testing.T) {
	ptr := func(n int) *int { return &n }
	cases := []struct {
		name string
		in   *int
		want int
	}{
		{"unset uses the default", nil, DefaultCutoffHour},
		{"zero is midnight, not unset", ptr(0), 0},
		{"in range is kept", ptr(13), 13},
		{"over range falls back", ptr(24), DefaultCutoffHour},
		{"negative falls back", ptr(-1), DefaultCutoffHour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveCutoff(tc.in); got != tc.want {
				t.Errorf("ResolveCutoff() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestLocation pins the fallback every caller relies on: dirty timezone data reads as UTC instead of failing.
func TestLocation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "UTC"},
		{"Not/AZone", "UTC"},
		{"Asia/Shanghai", "Asia/Shanghai"},
	}
	for _, tc := range cases {
		if got := Location(tc.in).String(); got != tc.want {
			t.Errorf("Location(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDayStartNextStart(t *testing.T) {
	plus8 := time.FixedZone("UTC+8", 8*3600)
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load New York: %v", err)
	}
	cases := []struct {
		name      string
		now       time.Time
		loc       *time.Location
		cutoff    int
		wantDay   string
		wantStart time.Time
		wantNext  time.Time
	}{
		{
			name: "before the cutoff belongs to the previous day",
			now:  time.Date(2026, 10, 2, 3, 59, 0, 0, plus8), loc: plus8, cutoff: 4,
			wantDay:   "2026-10-01",
			wantStart: time.Date(2026, 10, 1, 4, 0, 0, 0, plus8).UTC(),
			wantNext:  time.Date(2026, 10, 2, 4, 0, 0, 0, plus8).UTC(),
		},
		{
			name: "at the cutoff a new day begins",
			now:  time.Date(2026, 10, 2, 4, 0, 0, 0, plus8), loc: plus8, cutoff: 4,
			wantDay:   "2026-10-02",
			wantStart: time.Date(2026, 10, 2, 4, 0, 0, 0, plus8).UTC(),
			wantNext:  time.Date(2026, 10, 3, 4, 0, 0, 0, plus8).UTC(),
		},
		{
			name: "midnight cutoff",
			now:  time.Date(2026, 10, 2, 0, 30, 0, 0, plus8), loc: plus8, cutoff: 0,
			wantDay:   "2026-10-02",
			wantStart: time.Date(2026, 10, 2, 0, 0, 0, 0, plus8).UTC(),
			wantNext:  time.Date(2026, 10, 3, 0, 0, 0, 0, plus8).UTC(),
		},
		{
			name: "out-of-range cutoff behaves like the default",
			now:  time.Date(2026, 10, 2, 0, 30, 0, 0, plus8), loc: plus8, cutoff: 25,
			wantDay:   "2026-10-01",
			wantStart: time.Date(2026, 10, 1, 4, 0, 0, 0, plus8).UTC(),
			wantNext:  time.Date(2026, 10, 2, 4, 0, 0, 0, plus8).UTC(),
		},
		{
			name: "nil location reads as UTC",
			now:  time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC), loc: nil, cutoff: 4,
			wantDay:   "2026-10-02",
			wantStart: time.Date(2026, 10, 2, 4, 0, 0, 0, time.UTC),
			wantNext:  time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC),
		},
		{
			// The next day starts at the local cutoff hour even when daylight saving ends in between (a 25-hour day).
			name: "next start follows the calendar across a DST change",
			now:  time.Date(2026, 10, 31, 12, 0, 0, 0, newYork), loc: newYork, cutoff: 4,
			wantDay:   "2026-10-31",
			wantStart: time.Date(2026, 10, 31, 4, 0, 0, 0, newYork).UTC(),
			wantNext:  time.Date(2026, 11, 1, 4, 0, 0, 0, newYork).UTC(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Day(tc.now, tc.loc, tc.cutoff); got != tc.wantDay {
				t.Errorf("Day() = %q, want %q", got, tc.wantDay)
			}
			if got := Start(tc.now, tc.loc, tc.cutoff); !got.Equal(tc.wantStart) {
				t.Errorf("Start() = %v, want %v", got, tc.wantStart)
			}
			if got := NextStart(tc.now, tc.loc, tc.cutoff); !got.Equal(tc.wantNext) {
				t.Errorf("NextStart() = %v, want %v", got, tc.wantNext)
			}
		})
	}
}

func TestShift(t *testing.T) {
	cases := []struct {
		day   string
		delta int
		want  string
	}{
		{"2026-10-01", -1, "2026-09-30"},
		{"2026-12-31", 1, "2027-01-01"},
		{"not-a-day", 1, "not-a-day"},
	}
	for _, tc := range cases {
		if got := Shift(tc.day, tc.delta); got != tc.want {
			t.Errorf("Shift(%q, %d) = %q, want %q", tc.day, tc.delta, got, tc.want)
		}
	}
}
