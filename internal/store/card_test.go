package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// TestCardStoreByNoteScopesToNote covers that ByNote returns only the target note's cards, in ascending ordinal order.
func TestCardStoreByNoteScopesToNote(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "card_scope_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			noteA := noteOfKind(t, db, deckID, "basic_both")
			if _, err := notes.Create(ctx, noteA, map[string]any{"front": "qa", "back": "aa"}); err != nil {
				t.Fatalf("Create(noteA) error = %v", err)
			}
			noteB := noteOfKind(t, db, deckID, "basic")
			if _, err := notes.Create(ctx, noteB, map[string]any{"front": "qb", "back": "ab"}); err != nil {
				t.Fatalf("Create(noteB) error = %v", err)
			}

			gotA, err := cards.ByNote(ctx, noteA.ID)
			if err != nil {
				t.Fatalf("ByNote(noteA) error = %v", err)
			}
			if len(gotA) != 2 {
				t.Fatalf("ByNote(noteA) = %d cards, want 2", len(gotA))
			}
			if gotA[0].Ordinal > gotA[1].Ordinal {
				t.Errorf("ByNote(noteA) not ordered by ordinal: %+v", gotA)
			}
			if _, err := cards.ByID(ctx, gotA[0].ID); err != nil {
				t.Errorf("ByID() error = %v", err)
			}

			gotB, err := cards.ByNote(ctx, noteB.ID)
			if err != nil {
				t.Fatalf("ByNote(noteB) error = %v", err)
			}
			if len(gotB) != 1 || gotB[0].NoteID != noteB.ID {
				t.Errorf("ByNote(noteB) = %+v, want only noteB's single card", gotB)
			}
		})
	}
}

// TestCardStoreHidesCardsOfSoftDeletedNote proves that after soft-deleting a note its cards are filtered by the query
// layer, and that the filtering is not achieved by rewriting cards rows (the rows are still there).
func TestCardStoreHidesCardsOfSoftDeletedNote(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "card_hide_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)
			cards := NewCardStore(db)

			n := noteOfKind(t, db, deckID, "basic_both")
			created, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			firstID := created[0].ID

			if err := notes.Delete(ctx, n.ID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			if got, err := cards.ByNote(ctx, n.ID); err != nil || len(got) != 0 {
				t.Errorf("ByNote() after soft delete = %+v, %v; want empty slice, nil", got, err)
			}
			if _, err := cards.ByID(ctx, firstID); !IsNotFound(err) {
				t.Errorf("ByID() after soft delete error = %v, want ErrRecordNotFound", err)
			}

			// cards rows (and their ids) were neither rewritten nor deleted.
			var cardRow Card
			if err := db.Unscoped().First(&cardRow, "id = ?", firstID).Error; err != nil {
				t.Fatalf("card row vanished after soft delete: %v", err)
			}
			var noteRow Note
			if err := db.Unscoped().First(&noteRow, "id = ?", n.ID).Error; err != nil {
				t.Fatalf("note row vanished after soft delete: %v", err)
			}
			if !noteRow.DeletedAt.Valid {
				t.Error("note deleted_at is not set after Delete()")
			}
			if cardRow.DeletedAt.Valid {
				t.Error("card deleted_at was set by note soft delete, want query-layer filtering instead")
			}
		})
	}
}

// TestSyncCardsRejectsUnknownTemplateShape covers the defensive path: an empty template and a duplicate template
// are both rejected before anything is written (the Go-side guard for the (note_id, template) unique constraint).
func TestSyncCardsRejectsUnknownTemplateShape(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "sync_shape_owner")[0]
			deckID := seedDeck(t, db, owner)
			notes := NewNoteStore(db)

			n := noteOfKind(t, db, deckID, "basic")
			if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			emptyErr := db.Transaction(func(tx *gorm.DB) error {
				_, err := syncCards(tx, n.ID, []notetype.GeneratedCard{{Template: ""}}, time.Now().UTC())
				return err
			})
			if emptyErr == nil {
				t.Error("syncCards() accepted an empty template, want an error")
			}

			dupErr := db.Transaction(func(tx *gorm.DB) error {
				_, err := syncCards(tx, n.ID, []notetype.GeneratedCard{
					{Template: "reverse", Ordinal: 0},
					{Template: "reverse", Ordinal: 1},
				}, time.Now().UTC())
				return err
			})
			if dupErr == nil {
				t.Error("syncCards() accepted duplicate templates, want an error")
			}

			// neither failed sync may leave a half-written row.
			var rows int64
			if err := db.Unscoped().Model(&Card{}).Where("note_id = ?", n.ID).Count(&rows).Error; err != nil {
				t.Fatalf("count cards: %v", err)
			}
			if rows != 1 {
				t.Errorf("card rows after failed syncs = %d, want 1 (transaction must roll back)", rows)
			}
		})
	}
}
