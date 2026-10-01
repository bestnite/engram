package store

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/cardtype"
)

// TestCardStoreByNoteScopesToNote 覆盖 ByNote 只返回目标 note 的卡片，并按 ordinal 升序。
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

			noteA := &Note{DeckID: deckID, Kind: "basic_both"}
			if _, err := notes.Create(ctx, noteA, map[string]any{"front": "qa", "back": "aa"}); err != nil {
				t.Fatalf("Create(noteA) error = %v", err)
			}
			noteB := &Note{DeckID: deckID, Kind: "basic"}
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

// TestCardStoreHidesCardsOfSoftDeletedNote 证明软删除 note 后 cards 由查询层过滤，
// 且被过滤不是通过改写 cards 行实现的（行仍在）。
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

			n := &Note{DeckID: deckID, Kind: "basic_both"}
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

			// cards 行（及其 id）未被改写或删除。
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

// TestSyncCardsRejectsUnknownTemplateShape 覆盖防御路径：空 template 与重复 template
// 都在写库前被拒（(note_id, template) 唯一约束的 Go 侧防线）。
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

			n := &Note{DeckID: deckID, Kind: "basic"}
			if _, err := notes.Create(ctx, n, map[string]any{"front": "q", "back": "a"}); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			emptyErr := db.Transaction(func(tx *gorm.DB) error {
				_, err := syncCards(tx, n.ID, []cardtype.Card{{Template: ""}}, time.Now().UTC())
				return err
			})
			if emptyErr == nil {
				t.Error("syncCards() accepted an empty template, want an error")
			}

			dupErr := db.Transaction(func(tx *gorm.DB) error {
				_, err := syncCards(tx, n.ID, []cardtype.Card{
					{Template: "reverse", Ordinal: 0},
					{Template: "reverse", Ordinal: 1},
				}, time.Now().UTC())
				return err
			})
			if dupErr == nil {
				t.Error("syncCards() accepted duplicate templates, want an error")
			}

			// 两次失败的同步都不能留下半截行。
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
