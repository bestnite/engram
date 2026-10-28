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
			if before.Today == 0 || before.NewNotDue == 0 {
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

// TestReminderCandidatesSkipPausedDecks: a user whose only due cards sit in a paused deck gets no reminder,
// while another user's due cards still produce one.
func TestReminderCandidatesSkipPausedDecks(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			now := time.Now().UTC()
			active := seedReminderUser(t, db, "pause_active", "pause_active@example.com", now.Add(-time.Hour), false)
			paused := seedReminderUser(t, db, "pause_paused", "pause_paused@example.com", now.Add(-time.Hour), false)
			var deck Deck
			if err := db.Where("owner_user_id = ?", paused).Take(&deck).Error; err != nil {
				t.Fatalf("load deck: %v", err)
			}
			if _, err := NewDeckStore(db).SetPaused(ctx, paused, deck.ID, true, now); err != nil {
				t.Fatalf("SetPaused() error = %v", err)
			}

			got, err := ReminderCandidates(ctx, db, now)
			if err != nil {
				t.Fatalf("ReminderCandidates() error = %v", err)
			}
			ids := map[uint64]int64{}
			for _, c := range got {
				ids[c.ID] = c.DueCount
			}
			if ids[active] != 1 {
				t.Errorf("active user due count = %d, want 1 (candidates %+v)", ids[active], got)
			}
			if _, ok := ids[paused]; ok {
				t.Errorf("user with only a paused deck is a reminder candidate: %+v", got)
			}
		})
	}
}
