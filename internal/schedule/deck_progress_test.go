package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckProgressCountsStudiedAndRemaining runs on SQLite, and on PostgreSQL when TEST_PG_DSN is set:
// the counts come from a hand-written SUM(CASE ...) aggregate, and both databases must agree on it.
func TestDeckProgressCountsStudiedAndRemaining(t *testing.T) {
	for driver, db := range queueTestDatabases(t) {
		t.Run(driver, func(t *testing.T) { assertDeckProgress(t, db) })
	}
}

func assertDeckProgress(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	last := now.Add(-48 * time.Hour)
	future := now.Add(48 * time.Hour)

	mkDeck := func(owner uint64, name string) uint64 {
		t.Helper()
		p := store.NewPreset(owner, "preset-"+name)
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
		d := store.Deck{OwnerUserID: owner, Name: name, PresetID: p.ID, CreatedAt: now}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		return d.ID
	}
	suspend := func(userID, cardID uint64) {
		t.Helper()
		var n int64
		db.Model(&store.CardState{}).Where("card_id = ? AND user_id = ?", cardID, userID).Count(&n)
		if n == 0 {
			seedState(t, db, userID, cardID, "new", now, 0, 0, nil)
		}
		if err := db.Model(&store.CardState{}).Where("card_id = ? AND user_id = ?", cardID, userID).
			Update("suspended_at", now).Error; err != nil {
			t.Fatalf("suspend card: %v", err)
		}
	}

	mixed := mkDeck(1, "mixed")
	// Two cards without any state row and one with an explicit new row: all three are remaining.
	seedCard(t, db, mixed, "fresh", now)
	seedCard(t, db, mixed, "fresh", now)
	explicitNew := seedCard(t, db, mixed, "explicit-new", now)
	seedState(t, db, 1, explicitNew, "new", now, 0, 0, nil)
	// A buried new card is only postponed, so it is still remaining.
	buried := seedCard(t, db, mixed, "buried", now)
	seedState(t, db, 1, buried, "new", future, 0, 0, nil)
	// One card in each studied state.
	for _, state := range []string{"learning", "review", "relearning"} {
		id := seedCard(t, db, mixed, state, now)
		seedState(t, db, 1, id, state, future, 5.0, 5.0, &last)
	}
	// A suspended new card leaves remaining; a suspended studied card stays studied.
	suspendedNew := seedCard(t, db, mixed, "suspended-new", now)
	suspend(1, suspendedNew)
	suspendedStudied := seedCard(t, db, mixed, "suspended-review", now)
	seedState(t, db, 1, suspendedStudied, "review", future, 5.0, 5.0, &last)
	suspend(1, suspendedStudied)
	// A soft-deleted card and a card of a soft-deleted note count nowhere.
	deletedCard := seedCard(t, db, mixed, "deleted-card", now)
	seedState(t, db, 1, deletedCard, "review", future, 5.0, 5.0, &last)
	if err := db.Delete(&store.Card{}, deletedCard).Error; err != nil {
		t.Fatalf("delete card: %v", err)
	}
	deletedNoteCard := seedCard(t, db, mixed, "deleted-note", now)
	var c store.Card
	if err := db.First(&c, deletedNoteCard).Error; err != nil {
		t.Fatalf("load card: %v", err)
	}
	if err := db.Delete(&store.Note{}, c.NoteID).Error; err != nil {
		t.Fatalf("delete note: %v", err)
	}

	// A shared deck: user 2 has studied two cards; user 1 must still see both as remaining.
	shared := mkDeck(2, "shared")
	for range 2 {
		id := seedCard(t, db, shared, "shared", now)
		seedState(t, db, 2, id, "review", future, 5.0, 5.0, &last)
	}

	empty := mkDeck(1, "empty")

	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	tests := []struct {
		name   string
		userID uint64
		deckID uint64
		want   DeckProgress
	}{
		{"mixed deck for its owner", 1, mixed, DeckProgress{Studied: 4, Remaining: 4}},
		{"shared deck ignores another user's progress", 1, shared, DeckProgress{Studied: 0, Remaining: 2}},
		{"shared deck for the member who studied it", 2, shared, DeckProgress{Studied: 2, Remaining: 0}},
		{"mixed deck for a user with no state rows", 2, mixed, DeckProgress{Studied: 0, Remaining: 9}},
		{"deck without cards", 1, empty, DeckProgress{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := builder.DeckProgress(ctx, tc.userID, []uint64{mixed, shared, empty})
			if err != nil {
				t.Fatalf("DeckProgress() error = %v", err)
			}
			if got[tc.deckID] != tc.want {
				t.Errorf("DeckProgress()[%d] = %+v, want %+v", tc.deckID, got[tc.deckID], tc.want)
			}
		})
	}
}

func TestDeckProgressRejectsMissingUserAndAcceptsEmptyScope(t *testing.T) {
	db := newTestDB(t)
	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	if _, err := builder.DeckProgress(context.Background(), 0, []uint64{1}); err == nil {
		t.Error("DeckProgress(userID=0) error = nil, want an error")
	}
	got, err := builder.DeckProgress(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("DeckProgress(empty) error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("DeckProgress(empty) = %v, want an empty map", got)
	}
}
