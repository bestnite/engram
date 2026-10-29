package store

import (
	"context"
	"testing"
	"time"
)

// TestPausedDeckLeavesForecastAndRetroCheck pauses the only deck of the stats fixture: the unfiltered forecast (which the weekly
// digest reuses) must drop it, naming the deck must still count it, and the raw-table recomputation must agree with the page.
func TestPausedDeckLeavesForecastAndRetroCheck(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			f := seedStatsFixture(t, db)
			ctx := context.Background()
			stats := NewStatsStore(db)

			before, err := stats.DueForecast(ctx, 1, 0, statsNow, time.UTC, 4)
			if err != nil {
				t.Fatalf("DueForecast() error = %v", err)
			}
			if before.Today == 0 || before.New == 0 {
				t.Fatalf("fixture forecast %+v has nothing due; the case would pass vacuously", before)
			}
			if _, err := NewDeckStore(db).SetPaused(ctx, 1, f.deckID, true, statsNow); err != nil {
				t.Fatalf("SetPaused() error = %v", err)
			}

			tests := []struct {
				name   string
				deckID uint64
				want   DueForecast
			}{
				{"unfiltered forecast drops the paused deck", 0, DueForecast{}},
				{"naming the paused deck still counts it", f.deckID, before},
			}
			for _, tc := range tests {
				got, err := stats.DueForecast(ctx, 1, tc.deckID, statsNow, time.UTC, 4)
				if err != nil {
					t.Fatalf("%s: DueForecast() error = %v", tc.name, err)
				}
				if got != tc.want {
					t.Errorf("%s: DueForecast() = %+v, want %+v", tc.name, got, tc.want)
				}
			}

			drift, err := RetroCheck(ctx, db, 1, statsNow, retroToday, retroFrom, retroTo, time.UTC, 4)
			if err != nil {
				t.Fatalf("RetroCheck() error = %v", err)
			}
			if len(drift) != 0 {
				t.Errorf("RetroCheck() found drift with a paused deck: %v", drift)
			}
		})
	}
}
