package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// retroWindow is an inclusive range covering every review day in the fixtures (the fixture log entries all fall within 2026).
const (
	retroFrom  = "2026-01-01"
	retroTo    = "2026-12-31"
	retroToday = "2026-10-02"
)

// TestRetroCheckRecomputesPageNumbers is the positive acceptance: the stats page numbers and the result recomputed from the raw tables
// must match item by item, otherwise the drifting items are reported.
func TestRetroCheckRecomputesPageNumbers(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()
			drift, err := RetroCheck(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("RetroCheck() error = %v", err)
			}
			if len(drift) != 0 {
				t.Fatalf("RetroCheck() found drift: %v", drift)
			}
			page, err := PageNumbersFromStats(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("PageNumbersFromStats() error = %v", err)
			}
			// The values are non-zero, proving the check really runs over data instead of passing on an empty set.
			if page.ReviewToday == 0 || page.DueToday == 0 || page.RetentionTotal == 0 || page.CurveNew+page.CurveReview == 0 {
				t.Fatalf("fixture produced all-zero page numbers %+v; the check would pass vacuously", page)
			}
			t.Logf("page numbers = %+v", page)
		})
	}
}

// TestRetroCheckCatchesCorruptedAggregate is the negative acceptance: one aggregate value is broken on purpose,
// and the check must fail naming that number. What is broken here is the page side's review_volume.today -- the typical shape of "number drift"
// (the aggregate code broken while the raw tables are unchanged).
func TestRetroCheckCatchesCorruptedAggregate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			seedStatsFixture(t, db)
			ctx := context.Background()

			page, err := PageNumbersFromStats(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("PageNumbersFromStats() error = %v", err)
			}
			raw, err := RecomputePageNumbers(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("RecomputePageNumbers() error = %v", err)
			}
			if drift := DiffPageNumbers(page, raw); len(drift) != 0 {
				t.Fatalf("baseline should be clean, got drift %v", drift)
			}

			corrupted := page
			corrupted.ReviewToday++ // break one aggregate value
			drift := DiffPageNumbers(corrupted, raw)
			if len(drift) != 1 {
				t.Fatalf("corrupted aggregate produced %d drift(s) %v, want exactly 1", len(drift), drift)
			}
			if !strings.Contains(drift[0], "review_volume.today") {
				t.Fatalf("drift %q does not name the corrupted aggregate review_volume.today", drift[0])
			}
			t.Logf("corrupted aggregate detected: %s", drift[0])

			// Break the aggregate value of a different item too; the check must catch that as well.
			corrupted = page
			corrupted.DueNew += 99
			drift = DiffPageNumbers(corrupted, raw)
			if len(drift) != 1 || !strings.Contains(drift[0], "due_forecast.new") {
				t.Fatalf("corrupted DueNew -> drift %v, want due_forecast.new", drift)
			}
			t.Logf("second corrupted aggregate detected: %s", drift[0])
		})
	}
}
