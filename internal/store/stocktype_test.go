package store

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// stockNoteTypeID returns the deck's note type id for one engram stock kind, creating it when the deck has none.
// Tests that write notes name a note type, so this keeps the seeding of a note to one line.
func stockNoteTypeID(t *testing.T, db *gorm.DB, deckID uint64, kind string) uint64 {
	t.Helper()
	nt, err := NewNoteStore(db).EnsureStockNoteType(context.Background(), deckID, kind)
	if err != nil {
		t.Fatalf("ensure note type for kind %q: %v", kind, err)
	}
	return nt.ID
}

// noteOfKind builds an unsaved note of one engram stock kind for the deck.
func noteOfKind(t *testing.T, db *gorm.DB, deckID uint64, kind string) *Note {
	t.Helper()
	return &Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, kind))}
}
