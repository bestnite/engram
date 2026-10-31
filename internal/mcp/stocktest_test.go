package mcp

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
	"git.nite07.com/nite/engram/internal/store"
)

// createStockNoteType creates the deck's note type for one engram stock kind and returns it.
func createStockNoteType(t *testing.T, db *gorm.DB, deckID uint64, kind string) *store.NoteType {
	t.Helper()
	stock, ok := notetype.EngramStockByKind(kind)
	if !ok {
		t.Fatalf("unknown stock kind %q", kind)
	}
	types, err := store.NewNoteTypeStore(db).ListByDeck(context.Background(), deckID)
	if err != nil {
		t.Fatalf("list note types of deck %d: %v", deckID, err)
	}
	for i := range types {
		if cfg, err := types[i].Config(); err == nil && notetype.EngramStockKindFor(cfg) == kind {
			return &types[i]
		}
	}
	nt := &store.NoteType{DeckID: deckID, Name: stock.Name}
	if err := store.NewNoteTypeStore(db).Create(context.Background(), nt, stock.Config); err != nil {
		t.Fatalf("create note type for kind %q: %v", kind, err)
	}
	return nt
}

// stockNoteTypeID returns the deck's note type id for one engram stock kind, creating it when the deck has none.
func stockNoteTypeID(t *testing.T, db *gorm.DB, deckID uint64, kind string) uint64 {
	t.Helper()
	return createStockNoteType(t, db, deckID, kind).ID
}
