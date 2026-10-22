package store_test

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// TestReviewDayCutoffNormalizationConsistent is the regression case for 'inconsistent rules'.
//
// store.ReviewDayString is the DB-side review day (review_day) rule and schedule.ReviewDay is the scheduling-side
// review day rule: for the same cutoffHour both must compute the same day. 0 is midnight,
// only out-of-range values fall back to the default 4; an unset nil is resolved by ResolveCutoff before the computation is called.
func TestReviewDayCutoffNormalizationConsistent(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	// local 00:30 tells midnight apart from the default 4; out-of-range input must agree with the default.
	now := time.Date(2026, 10, 2, 0, 30, 0, 0, loc)
	defaultDay := schedule.ReviewDay(now, loc, schedule.DefaultDayCutoffHour)

	cases := []struct {
		name   string
		cutoff int
		want   string
	}{
		{"zero means midnight", 0, "2026-10-02"},
		{"over range -> default", 25, defaultDay},
		{"negative -> default", -1, defaultDay},
		{"in-range cutoff is kept", 13, schedule.ReviewDay(now, loc, 13)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStore := store.ReviewDayString(now, loc, tc.cutoff)
			gotSched := schedule.ReviewDay(now, loc, tc.cutoff)
			if gotStore != tc.want {
				t.Errorf("store.ReviewDayString(cutoff=%d) = %q, want %q", tc.cutoff, gotStore, tc.want)
			}
			if gotSched != tc.want {
				t.Errorf("schedule.ReviewDay(cutoff=%d) = %q, want %q", tc.cutoff, gotSched, tc.want)
			}
			if gotStore != gotSched {
				t.Errorf("review-day start disagrees for cutoff=%d: store=%q schedule=%q",
					tc.cutoff, gotStore, gotSched)
			}
		})
	}
}

// TestLoadLocationFallback pins the contract of store.LoadLocation, the single timezone resolution entry point: an empty string and an invalid IANA name
// both fall back to UTC, a valid name resolves unchanged. After C2 collapsed five timezone resolutions into this one, every caller (web/api/reminder/
// digest/schedule) depends on this fallback behavior, so a case here must guard it.
func TestLoadLocationFallback(t *testing.T) {
	if got := store.LoadLocation(""); got != time.UTC {
		t.Errorf("LoadLocation(\"\") = %v, want UTC", got)
	}
	if got := store.LoadLocation("Not/AZone"); got != time.UTC {
		t.Errorf("LoadLocation(\"Not/AZone\") = %v, want UTC", got)
	}
	if got := store.LoadLocation("Asia/Shanghai"); got.String() != "Asia/Shanghai" {
		t.Errorf("LoadLocation(\"Asia/Shanghai\") = %v, want Asia/Shanghai", got)
	}
}
