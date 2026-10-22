package schedule

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestQueueHonoursDeckDailyCaps is an acceptance case: the daily caps must come from the deck columns,
// not from the hard-coded defaults in QueueOptions. The deck is set to 2/1 (far below the default 20/200),
// so if the defaults were still used, the assertions below would fail immediately.
func TestQueueHonoursDeckDailyCaps(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	p := store.NewPreset(1, "preset")
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: 1, Name: "Capped", Description: "",
		NewPerDay: 2, ReviewsPerDay: 1, PresetID: p.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}

	// 3 due review cards.
	for i := 0; i < 3; i++ {
		id := seedCard(t, db, deck.ID, string(rune('A'+i)), now)
		last := now.Add(-24 * time.Hour)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}
	// 5 new cards.
	for i := 0; i < 5; i++ {
		seedCard(t, db, deck.ID, string(rune('a'+i)), now.Add(time.Duration(i)*time.Minute))
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(context.Background(), 1, QueueOptions{
		DeckID: deck.ID, Now: now, Location: time.UTC, NewOrder: NewOrderCreated,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	reviews, news := 0, 0
	for _, it := range items {
		switch it.Kind {
		case QueueReview:
			reviews++
		case QueueNew:
			news++
		}
	}
	if reviews != 1 {
		t.Errorf("review cards = %d, want 1 (deck reviews_per_day)", reviews)
	}
	if news != 2 {
		t.Errorf("new cards = %d, want 2 (deck new_per_day)", news)
	}
}

// TestQueueCapsExplicitOverrideBeatsDeck asserts explicit overrides still take precedence over the deck settings,
// and existing callers (passing positive NewPerDay / ReviewsPerDay) behave unchanged.
func TestQueueCapsExplicitOverrideBeatsDeck(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	p := store.NewPreset(1, "preset")
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: 1, Name: "Capped",
		NewPerDay: 2, ReviewsPerDay: 1, PresetID: p.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	for i := 0; i < 5; i++ {
		seedCard(t, db, deck.ID, string(rune('a'+i)), now.Add(time.Duration(i)*time.Minute))
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(context.Background(), 1, QueueOptions{
		DeckID: deck.ID, Now: now, Location: time.UTC, NewOrder: NewOrderCreated,
		NewPerDay: 5,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	news := 0
	for _, it := range items {
		if it.Kind == QueueNew {
			news++
		}
	}
	if news != 5 {
		t.Errorf("new cards = %d, want 5 (explicit override beats the deck cap of 2)", news)
	}
}
