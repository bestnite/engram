package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestNewCardQuotaIsCountedPerDeck is the allowance-semantics acceptance test: new_per_day is defined as a
// **deck-level** setting, so within a single-deck scope "introduced today" may only count that deck's records.
//
// Before the fix it counted across the whole library: exhausting one deck's new-card allowance would block another deck's new cards too, and the symptom users actually saw
// was "the deck list shows 6 due cards, but clicking in says there are no cards due right now". Now the whole-library / multi-deck scope also sums each deck's
// own allowance (new_per_day is a deck-level column).
func TestNewCardQuotaIsCountedPerDeck(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	day := ReviewDay(now, time.UTC, DefaultDayCutoffHour)

	// Two decks, each with a daily new-card cap of 1.
	deckA := seedDeckWithNewCap(t, db, now, "A", 1)
	deckB := seedDeckWithNewCap(t, db, now, "B", 1)

	// Deck A has already introduced 1 new card today (allowance exhausted); both decks still have 1 new card left.
	// The introduced card needs a state row (learning, due in the future), otherwise it is still a "new card" and would be queued again.
	usedInA := seedCard(t, db, deckA, "used", now)
	seedReview(t, db, 1, usedInA, day, int(StateNew), now.Add(-time.Hour))
	seedState(t, db, 1, usedInA, "learning", now.Add(time.Hour), 1.0, 5.0, nil)
	seedCard(t, db, deckA, "freshA", now.Add(time.Minute))
	seedCard(t, db, deckB, "freshB", now.Add(2*time.Minute))

	s := mustScheduler(t, testPreset(t))
	build := func(opts QueueOptions) []QueueItem {
		t.Helper()
		opts.Now = now
		opts.Location = time.UTC
		opts.NewOrder = NewOrderCreated
		items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(context.Background(), 1, opts)
		if err != nil {
			t.Fatalf("Build(%+v) error = %v", opts, err)
		}
		return items
	}

	// Deck B: the allowance A used must not count against it — its own new card must come out.
	if got := build(QueueOptions{DeckID: deckB}); len(got) != 1 || got[0].DeckID != deckB {
		t.Errorf("deck B queue = %+v, want exactly its own new card (deck A's usage must not count)", got)
	}
	// Deck A: its own allowance is exhausted — not a single new card comes out.
	if got := build(QueueOptions{DeckID: deckA}); len(got) != 0 {
		t.Errorf("deck A queue = %+v, want empty (its own daily new-card cap is used up)", got)
	}
	// Whole-library scope: sum each deck's own allowance — B yields 1, A yields 0 since its allowance is exhausted.
	if got := build(QueueOptions{}); len(got) != 1 || got[0].DeckID != deckB {
		t.Errorf("all-decks queue = %+v, want only deck B's 1 new card (per-deck budgets summed)", got)
	}
}

// seedDeckWithNewCap creates a preset + private deck and sets the daily new-card cap explicitly.
func seedDeckWithNewCap(t *testing.T, db *gorm.DB, now time.Time, name string, newPerDay int) uint64 {
	t.Helper()
	p := store.NewPreset(1, "preset-"+name)
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{
		OwnerUserID: 1, Name: name, Description: "",
		NewPerDay: newPerDay, ReviewsPerDay: 200, PresetID: p.ID, CreatedAt: now,
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}
