package store

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// stockNoteTypeID returns the deck's note type id for one engram stock kind, creating it when the deck has none.
// Tests that write notes name a note type, so this keeps the seeding of a note to one line.
func stockNoteTypeID(t *testing.T, db *gorm.DB, deckID uint64, kind string) uint64 {
	t.Helper()
	stock, ok := notetype.EngramStockByKind(kind)
	if !ok {
		t.Fatalf("unknown stock kind %q", kind)
	}
	types, err := NewNoteTypeStore(db).ListByDeck(context.Background(), deckID)
	if err != nil {
		t.Fatalf("list note types of deck %d: %v", deckID, err)
	}
	for i := range types {
		if cfg, err := types[i].Config(); err == nil && notetype.EngramStockKindFor(cfg) == kind {
			return types[i].ID
		}
	}
	nt := &NoteType{DeckID: deckID, Name: stock.Name}
	if err := NewNoteTypeStore(db).Create(context.Background(), nt, stock.Config); err != nil {
		t.Fatalf("create note type for kind %q: %v", kind, err)
	}
	return nt.ID
}

// noteOfKind builds an unsaved note of one engram stock kind for the deck.
func noteOfKind(t *testing.T, db *gorm.DB, deckID uint64, kind string) *Note {
	t.Helper()
	return &Note{DeckID: deckID, NoteTypeID: Ptr(stockNoteTypeID(t, db, deckID, kind))}
}
