package store

import (
	"context"
	"testing"
	"time"
)

// TestRemoveUnresolvableCards is the acceptance of migration 0019: a card whose template its note type cannot
// resolve leaves the queue as a soft-deleted row with its progress intact, every resolvable card stays, and a
// second run writes nothing.
func TestRemoveUnresolvableCards(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := AutoMigrate(context.Background(), db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			cloze := createType(t, db, 7, "Cloze", clozeConfig())
			normal := createType(t, db, 7, "Basic", noteTypeConfig("Card 1"))
			clozeNote := Note{DeckID: 7, NoteTypeID: Ptr(cloze.ID), FieldsJSON: `{"c-text":"{{c1::你好}}"}`}
			normalNote := Note{DeckID: 7, NoteTypeID: Ptr(normal.ID), FieldsJSON: `{"f-front":"q","f-back":"a"}`}
			for _, note := range []*Note{&clozeNote, &normalNote} {
				if err := db.Create(note).Error; err != nil {
					t.Fatalf("insert note: %v", err)
				}
			}
			now := time.Now().UTC()
			cards := map[string]*Card{
				"cloze:0": {NoteID: clozeNote.ID, Template: "cloze:0", Ordinal: 0, CreatedAt: now},
				"cloze:1": {NoteID: clozeNote.ID, Template: "cloze:1", Ordinal: 0, CreatedAt: now},
				"t-card":  {NoteID: normalNote.ID, Template: "t-card", Ordinal: 0, CreatedAt: now},
				"t-gone":  {NoteID: normalNote.ID, Template: "t-gone", Ordinal: 1, CreatedAt: now},
			}
			for name, card := range cards {
				if err := db.Create(card).Error; err != nil {
					t.Fatalf("insert card %s: %v", name, err)
				}
			}
			state := CardState{CardID: cards["cloze:0"].ID, UserID: 3, State: "review", Version: 4}
			if err := db.Create(&state).Error; err != nil {
				t.Fatalf("insert card state: %v", err)
			}

			if err := removeUnresolvableCards(db); err != nil {
				t.Fatalf("migration: %v", err)
			}
			wantRemoved := map[string]bool{"cloze:0": true, "cloze:1": false, "t-card": false, "t-gone": true}
			for name, removed := range wantRemoved {
				var row Card
				if err := db.Unscoped().First(&row, cards[name].ID).Error; err != nil {
					t.Fatalf("load card %s: %v", name, err)
				}
				if row.DeletedAt.Valid != removed {
					t.Errorf("card %s soft-deleted = %v, want %v", name, row.DeletedAt.Valid, removed)
				}
			}
			var kept CardState
			if err := db.Where("card_id = ? AND user_id = ?", cards["cloze:0"].ID, 3).First(&kept).Error; err != nil {
				t.Fatalf("progress of the removed card is gone: %v", err)
			}
			if kept.Version != 4 {
				t.Errorf("progress version = %d, want 4", kept.Version)
			}

			var removedAt Card
			if err := db.Unscoped().First(&removedAt, cards["cloze:0"].ID).Error; err != nil {
				t.Fatalf("load removed card: %v", err)
			}
			if err := removeUnresolvableCards(db); err != nil {
				t.Fatalf("second run: %v", err)
			}
			var again Card
			if err := db.Unscoped().First(&again, cards["cloze:0"].ID).Error; err != nil {
				t.Fatalf("load removed card after second run: %v", err)
			}
			if !again.DeletedAt.Time.Equal(removedAt.DeletedAt.Time) {
				t.Errorf("second run rewrote deleted_at: %v -> %v", removedAt.DeletedAt.Time, again.DeletedAt.Time)
			}
		})
	}
}
