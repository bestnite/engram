package api

import (
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// stockNoteTypeID returns the deck's note type id for one engram stock kind, creating it when the deck has none.
func stockNoteTypeID(t *testing.T, db *gorm.DB, deckID uint64, kind string) uint64 {
	t.Helper()
	nt, err := store.NewNoteStore(db).EnsureStockNoteType(t.Context(), deckID, kind)
	if err != nil {
		t.Fatalf("ensure note type for kind %q: %v", kind, err)
	}
	return nt.ID
}
