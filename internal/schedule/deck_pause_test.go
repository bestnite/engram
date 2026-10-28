package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestPausedDeckLeavesOnlyTheDefaultQueue: a paused deck drops out of the whole-library queue of the user who paused it,
// still serves its cards when named, and leaves the queue of every other member untouched.
func TestPausedDeckLeavesOnlyTheDefaultQueue(t *testing.T) {
	for driver, db := range queueTestDatabases(t) {
		t.Run(driver, func(t *testing.T) { assertPausedDeckQueue(t, db) })
	}
}

func assertPausedDeckQueue(t *testing.T, db *gorm.DB) {
	ctx := context.Background()
	now := time.Now().UTC()
	last := now.Add(-48 * time.Hour)
	decks := store.NewDeckStore(db)

	mkDeck := func(name string) uint64 {
		t.Helper()
		p := store.NewPreset(1, "preset-"+name)
		p.EnableFuzz = boolPtr(false)
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
		d := store.Deck{OwnerUserID: 1, Name: name, PresetID: p.ID, CreatedAt: now}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		return d.ID
	}
	shared := mkDeck("shared")
	other := mkDeck("other")
	if err := db.Create(&store.DeckGrant{DeckID: shared, UserID: 2, Role: store.RoleReader}).Error; err != nil {
		t.Fatalf("grant: %v", err)
	}
	// One due review card per deck for each user.
	for _, deckID := range []uint64{shared, other} {
		id := seedCard(t, db, deckID, "forward", now)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
		seedState(t, db, 2, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}

	// User 1 pauses "other"; user 2 pauses "shared".
	if _, err := decks.SetPaused(ctx, 1, other, true, now); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := decks.SetPaused(ctx, 2, shared, true, now); err != nil {
		t.Fatalf("pause: %v", err)
	}

	builder := NewQueueBuilder(db, decks, mustScheduler(t, testPreset(t)))
	tests := []struct {
		name      string
		userID    uint64
		opts      QueueOptions
		wantDecks map[uint64]int
	}{
		{"owner default queue skips the owner's pause and ignores the member's", 1, QueueOptions{}, map[uint64]int{shared: 1}},
		{"owner naming the paused deck still studies it", 1, QueueOptions{DeckID: other}, map[uint64]int{other: 1}},
		{"owner batch review including the paused deck", 1, QueueOptions{DeckIDs: []uint64{shared, other}}, map[uint64]int{shared: 1, other: 1}},
		{"member default queue is empty", 2, QueueOptions{}, map[uint64]int{}},
		{"member naming the paused deck still studies it", 2, QueueOptions{DeckID: shared}, map[uint64]int{shared: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Now = now
			tc.opts.Location = time.UTC
			items, err := builder.Build(ctx, tc.userID, tc.opts)
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			got := map[uint64]int{}
			for _, it := range items {
				got[it.DeckID]++
			}
			if len(got) != len(tc.wantDecks) {
				t.Fatalf("Build() decks = %v, want %v", got, tc.wantDecks)
			}
			for id, n := range tc.wantDecks {
				if got[id] != n {
					t.Errorf("Build() deck %d items = %d, want %d (all %v)", id, got[id], n, got)
				}
			}
		})
	}
}
