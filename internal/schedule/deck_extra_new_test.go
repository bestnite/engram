package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/reviewday"
	"git.nite07.com/nite/engram/internal/store"
)

// extraNewDrivers returns the databases to run: SQLite always runs; PostgreSQL runs too when TEST_PG_DSN is set.
// The added allowance is written by an accumulating upsert via clause.OnConflict, so both drivers' syntax and semantics must be covered.
func extraNewDrivers() map[string]func(t *testing.T) *gorm.DB {
	return map[string]func(t *testing.T) *gorm.DB{
		"sqlite": newTestDB,
		"postgres": func(t *testing.T) *gorm.DB {
			db, ok := pgtest.Open(t)
			if !ok {
				t.Skipf("%s not set: skipping the PostgreSQL path", pgtest.EnvDSN)
			}
			if err := db.AutoMigrate(store.AllModels()...); err != nil {
				t.Fatalf("AutoMigrate: %v", err)
			}
			return db
		},
	}
}

// TestExtraNewRaisesTodaysNewBudget covers the "another round" allowance rule: today's added allowance stacks on top of the daily cap,
// with Build, DeckCounts and DeckBudgets sharing the source; an addition from another review day has no effect; an unlimited deck is unaffected by the addition.
func TestExtraNewRaisesTodaysNewBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	day := reviewday.Day(now, time.UTC, reviewday.DefaultCutoffHour)
	yesterday := reviewday.Day(now.Add(-24*time.Hour), time.UTC, reviewday.DefaultCutoffHour)
	at := now.Add(-time.Hour)
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)

	cases := []struct {
		name string
		// newPerDay is the deck cap; introduced is the number of new cards introduced today; fresh is the number of new cards not studied yet.
		newPerDay, introduced, fresh int
		// extras are the added allowances to write, keyed by review day.
		extras        map[string]int
		wantNew       int
		wantExtra     int
		wantUnlimited bool
	}{
		{name: "goal reached without extra", newPerDay: 2, introduced: 2, fresh: 5, wantNew: 0},
		{name: "extra opens another batch", newPerDay: 2, introduced: 2, fresh: 5, extras: map[string]int{day: 2}, wantNew: 2, wantExtra: 2},
		{name: "smaller extra opens a smaller batch", newPerDay: 2, introduced: 2, fresh: 5, extras: map[string]int{day: 1}, wantNew: 1, wantExtra: 1},
		{name: "extra capped by cards left in deck", newPerDay: 2, introduced: 2, fresh: 1, extras: map[string]int{day: 10}, wantNew: 1, wantExtra: 10},
		{name: "yesterday's extra does not apply", newPerDay: 2, introduced: 2, fresh: 5, extras: map[string]int{yesterday: 3}, wantNew: 0},
		{name: "unlimited deck ignores extra", newPerDay: 0, introduced: 2, fresh: 3, extras: map[string]int{day: 1}, wantNew: 3, wantExtra: 1, wantUnlimited: true},
	}
	for driver, open := range extraNewDrivers() {
		for _, tc := range cases {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				db := open(t)
				deckID := seedDeckCaps(t, db, now, tc.newPerDay, 0)
				for i := 0; i < tc.introduced; i++ {
					id := seedCard(t, db, deckID, "introduced", now)
					seedReview(t, db, 1, id, day, int(StateNew), at)
					seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
				}
				for i := 0; i < tc.fresh; i++ {
					seedCard(t, db, deckID, "fresh", now)
				}
				// Write yesterday's addition first, then today's: AddDailyExtraNew also deletes rows from earlier review days along the way.
				if n, ok := tc.extras[yesterday]; ok {
					if _, err := store.AddDailyExtraNew(ctx, db, deckID, 1, yesterday, n); err != nil {
						t.Fatalf("AddDailyExtraNew(yesterday) error = %v", err)
					}
				}
				if n, ok := tc.extras[day]; ok {
					if _, err := store.AddDailyExtraNew(ctx, db, deckID, 1, day, n); err != nil {
						t.Fatalf("AddDailyExtraNew(today) error = %v", err)
					}
				}

				builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
				opts := QueueOptions{Now: now, DeckID: deckID}
				items, err := builder.Build(ctx, 1, opts)
				if err != nil {
					t.Fatalf("Build() error = %v", err)
				}
				if got := countKind(items, QueueNew); got != tc.wantNew {
					t.Errorf("Build() new cards = %d, want %d", got, tc.wantNew)
				}
				counts, err := builder.DeckCounts(ctx, 1, []uint64{deckID}, QueueOptions{Now: now})
				if err != nil {
					t.Fatalf("DeckCounts() error = %v", err)
				}
				if got := counts[deckID].New; got != tc.wantNew {
					t.Errorf("DeckCounts() new = %d, want %d", got, tc.wantNew)
				}
				budgets, err := builder.DeckBudgets(ctx, 1, []uint64{deckID}, QueueOptions{Now: now})
				if err != nil {
					t.Fatalf("DeckBudgets() error = %v", err)
				}
				b := budgets[deckID]
				if b.NewExtra != tc.wantExtra || b.NewUnlimited != tc.wantUnlimited {
					t.Errorf("DeckBudgets() extra=%d unlimited=%v, want extra=%d unlimited=%v", b.NewExtra, b.NewUnlimited, tc.wantExtra, tc.wantUnlimited)
				}
				if b.NewPerDay != tc.newPerDay {
					t.Errorf("DeckBudgets() NewPerDay = %d, want the unextended limit %d", b.NewPerDay, tc.newPerDay)
				}
			})
		}
	}
}

// TestAddDailyExtraNewAccumulatesAndDropsStaleDays asserts additions for the same review day accumulate,
// and that writing a new review day deletes earlier review days' rows, leaving exactly one row per (deck, user) pair.
func TestAddDailyExtraNewAccumulatesAndDropsStaleDays(t *testing.T) {
	for driver, open := range extraNewDrivers() {
		t.Run(driver, func(t *testing.T) { testAddDailyExtraNew(t, open(t)) })
	}
}

func testAddDailyExtraNew(t *testing.T, db *gorm.DB) {
	ctx := context.Background()
	deckID := seedDeckCaps(t, db, time.Now().UTC(), 5, 0)

	steps := []struct {
		day       string
		n         int
		wantTotal int
	}{
		{"2026-01-01", 5, 5},
		{"2026-01-01", 3, 8},
		{"2026-01-02", 4, 4},
	}
	for _, s := range steps {
		got, err := store.AddDailyExtraNew(ctx, db, deckID, 1, s.day, s.n)
		if err != nil {
			t.Fatalf("AddDailyExtraNew(%s, %d) error = %v", s.day, s.n, err)
		}
		if got != s.wantTotal {
			t.Errorf("AddDailyExtraNew(%s, %d) = %d, want %d", s.day, s.n, got, s.wantTotal)
		}
	}
	var rows int64
	if err := db.Model(&store.DeckDailyExtra{}).Where("deck_id = ? AND user_id = ?", deckID, 1).Count(&rows).Error; err != nil {
		t.Fatalf("count daily extras: %v", err)
	}
	if rows != 1 {
		t.Errorf("daily extra rows = %d, want 1 (stale days dropped)", rows)
	}
	// Two users' additions do not affect one another.
	if got, err := store.AddDailyExtraNew(ctx, db, deckID, 2, "2026-01-02", 1); err != nil || got != 1 {
		t.Errorf("AddDailyExtraNew(other user) = %d, %v; want 1, nil", got, err)
	}
	if _, err := store.AddDailyExtraNew(ctx, db, deckID, 1, "2026-01-02", 0); err == nil {
		t.Errorf("AddDailyExtraNew(n=0) error = nil, want a rejection")
	}
}

// TestNewCardsAvailableIgnoresBudget asserts NewCardsAvailable counts all introducible new cards in the deck:
// not truncated by the daily allowance, excluding suspended cards, and when tagged only counting cards carrying those tags.
func TestNewCardsAvailableIgnoresBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := newTestDB(t)
	deckID := seedDeckCaps(t, db, now, 1, 0)
	seedTaggedCard(t, db, deckID, []string{"a"}, now)
	seedTaggedCard(t, db, deckID, []string{"a", "b"}, now)
	seedTaggedCard(t, db, deckID, []string{"b"}, now)
	suspended := seedTaggedCard(t, db, deckID, []string{"a"}, now)
	at := now.Add(-time.Hour)
	if err := db.Create(&store.CardState{UserID: 1, CardID: suspended, State: StateNew.String(), SuspendedAt: &at}).Error; err != nil {
		t.Fatalf("suspend card: %v", err)
	}

	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	cases := []struct {
		name string
		tags []string
		want int
	}{
		{"whole deck", nil, 3},
		{"one tag", []string{"a"}, 2},
		{"union of tags", []string{"a", "b"}, 3},
		{"unknown tag", []string{"zzz"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := builder.NewCardsAvailable(ctx, 1, deckID, tc.tags, now)
			if err != nil {
				t.Fatalf("NewCardsAvailable() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("NewCardsAvailable() = %d, want %d", got, tc.want)
			}
		})
	}
}
