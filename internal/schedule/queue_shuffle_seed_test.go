package schedule

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
)

// This file pins down the **determinism** of the new-card shuffle: the seed is derived from (user, review day), not from the build instant.
//
// The pre-fix shape was `rand.New(rand.NewSource(now.UnixNano()))`, and production callers never set
// QueueOptions.Rand, so every page refresh within a day changed the new-card order — the order the learner saw was unstable
// and reported problems could not be reproduced.

// buildNewOrder creates a deck with n new cards and returns the order of the new cards in this build.
func buildNewOrder(t *testing.T, userID uint64, now time.Time, n int) []uint64 {
	t.Helper()
	db := newTestDB(t)
	deckID := seedDeck(t, db, now)
	for i := 0; i < n; i++ {
		seedCard(t, db, deckID, "forward", now.Add(time.Duration(i)*time.Minute))
	}
	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	opts := QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC,
		NewPerDay: n + 1, ReviewsPerDay: 0, NewOrder: NewOrderRandom,
	}
	items, err := builder.Build(context.Background(), userID, opts)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	out := make([]uint64, 0, len(items))
	for _, it := range items {
		if it.Kind == QueueNew {
			out = append(out, it.CardID)
		}
	}
	if len(out) != n {
		t.Fatalf("built %d new cards, want %d", len(out), n)
	}
	return out
}

// TestNewOrderIsStableWithinTheSameReviewDay asserts that repeated builds within the same day (even at different instants, different minutes)
// yield exactly the same permutation — that is what "stable, reproducible order" means.
func TestNewOrderIsStableWithinTheSameReviewDay(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	first := buildNewOrder(t, 1, now, 12)
	// Another instant on the same day, within the 04:00 cutoff (including nanosecond-level differences).
	later := buildNewOrder(t, 1, now.Add(7*time.Hour+123*time.Millisecond), 12)
	if len(first) != len(later) {
		t.Fatalf("lengths differ: %d vs %d", len(first), len(later))
	}
	for i := range first {
		if first[i] != later[i] {
			t.Fatalf("new-card order changed within one review day: %v vs %v", first, later)
		}
	}
}

// TestNewOrderDiffersAcrossReviewDays asserts a different permutation is used across review days (avoiding always studying only the first few).
//
// The seed is a pure function of the inputs, so this assertion is deterministic: the same two days always give the same conclusion, never flipping intermittently.
func TestNewOrderDiffersAcrossReviewDays(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	day1 := buildNewOrder(t, 1, now, 12)
	day2 := buildNewOrder(t, 1, now.Add(24*time.Hour), 12)
	if sameOrder(day1, day2) {
		t.Errorf("the same permutation on two review days: %v", day1)
	}
}

// TestNewOrderDiffersAcrossUsers asserts two users do not share the same permutation on the same day (otherwise users would start meeting
// the same batch of cards in the same order, weakening the impression of shuffling).
func TestNewOrderDiffersAcrossUsers(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	a := buildNewOrder(t, 1, now, 12)
	b := buildNewOrder(t, 2, now, 12)
	if sameOrder(a, b) {
		t.Errorf("two users got the same permutation on the same day: %v", a)
	}
}

// TestExplicitRandStillWins asserts an explicitly passed random source is not replaced (the contract for tests and explicit entry points).
func TestExplicitRandStillWins(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	const n = 12
	for i := 0; i < n; i++ {
		seedCard(t, db, deckID, "forward", now.Add(time.Duration(i)*time.Minute))
	}
	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	// Both builds share the same explicit random source: the second continues the previous sequence, so the order differs.
	rng := rand.New(rand.NewSource(42))
	order := func() []uint64 {
		items, err := builder.Build(context.Background(), 1, QueueOptions{
			DeckID: deckID, Now: now, Location: time.UTC,
			NewPerDay: n + 1, ReviewsPerDay: 0, NewOrder: NewOrderRandom, Rand: rng,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		out := make([]uint64, 0, n)
		for _, it := range items {
			if it.Kind == QueueNew {
				out = append(out, it.CardID)
			}
		}
		return out
	}
	if sameOrder(order(), order()) {
		t.Error("an explicit Rand should be consumed as-is, not ignored")
	}
}

// sameOrder compares two orders positionally.
func sameOrder(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestNewOrderRandomPicksBeyondTheOldestCards asserts the random order affects "which cards are drawn", not just the
// presentation order that day: with 40 new cards in the deck and a daily cap of 5, the ones drawn must not always be the 5 earliest created.
// The seed is determined by (user, review day), so the conclusion is deterministic for every day.
func TestNewOrderRandomPicksBeyondTheOldestCards(t *testing.T) {
	for driver, db := range queueTestDatabases(t) {
		t.Run(driver, func(t *testing.T) { assertRandomPicksBeyondOldest(t, db) })
	}
}

// queueTestDatabases returns the SQLite database, plus the PostgreSQL database when TEST_PG_DSN is set:
// the sort key is a hand-written SQL expression, and both databases must produce the same semantics.
func queueTestDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	out := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if pg, ok := pgtest.Open(t); ok {
		if err := pg.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatalf("AutoMigrate postgres: %v", err)
		}
		out["postgres"] = pg
	}
	return out
}

func assertRandomPicksBeyondOldest(t *testing.T, db *gorm.DB) {
	t.Helper()
	const total, perDay = 40, 5
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	oldest := map[uint64]bool{}
	for i := 0; i < total; i++ {
		id := seedCard(t, db, deckID, "forward", now.Add(time.Duration(i)*time.Minute))
		if i < perDay {
			oldest[id] = true
		}
	}
	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	pick := func(day int, order NewOrder) []uint64 {
		items, err := builder.Build(context.Background(), 1, QueueOptions{
			DeckID: deckID, Now: now.Add(time.Duration(day) * 24 * time.Hour), Location: time.UTC,
			NewPerDay: perDay, ReviewsPerDay: 0, NewOrder: order,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		var out []uint64
		for _, it := range items {
			if it.Kind == QueueNew {
				out = append(out, it.CardID)
			}
		}
		if len(out) != perDay {
			t.Fatalf("day %d: built %d new cards, want %d", day, len(out), perDay)
		}
		return out
	}

	// Control group: creation order is exactly the 5 earliest cards.
	for _, id := range pick(0, NewOrderCreated) {
		if !oldest[id] {
			t.Fatalf("NewOrderCreated picked card %d, which is not among the oldest %d", id, perDay)
		}
	}
	for day := 0; day < 3; day++ {
		outside := 0
		for _, id := range pick(day, NewOrderRandom) {
			if !oldest[id] {
				outside++
			}
		}
		if outside == 0 {
			t.Errorf("day %d: NewOrderRandom picked exactly the oldest %d cards", day, perDay)
		}
	}
}
