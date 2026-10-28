package web

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedReviewDeck creates a deck that uses a valid scheduling preset (for review tests only).
// Unlike seedDeck: it goes through store.NewPreset + PresetStore.Create to get a valid retention and maximum interval,
// otherwise FSRS would produce meaningless scheduling from zero-valued parameters.
func seedReviewDeck(t *testing.T, db *gorm.DB, ownerID uint64, name string) *store.Deck {
	t.Helper()
	preset := store.NewPreset(ownerID, "review preset")
	if err := store.NewPresetStore(db).Create(context.Background(), &preset); err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{OwnerUserID: ownerID, Name: name, PresetID: preset.ID}
	if err := store.NewDeckStore(db).Create(context.Background(), &deck); err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return &deck
}
