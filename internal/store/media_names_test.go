package store

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// TestMediaNameStore covers the deck-scoped name set: (deck_id, name) upserts instead of duplicating, the schema enforces
// uniqueness, the same name in another deck is a different file, and NFC spellings resolve to one row.
func TestMediaNameStore(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "media_name_owner")[0]
			deckA := seedDeck(t, db, owner)
			deckB := seedDeck(t, db, owner)
			media := NewMediaNameStore(db)

			if !db.Migrator().HasIndex(&MediaName{}, "idx_media_names_deck_name") {
				t.Fatal("media_names has no (deck_id, name) unique index; the uniqueness must be in the schema")
			}

			// Upsert creates, then a second upsert for the same (deck, name) repoints the row to the new content.
			if err := media.Upsert(ctx, deckA, "pic.png", "sha-one"); err != nil {
				t.Fatalf("Upsert() error = %v", err)
			}
			got, err := media.ByName(ctx, deckA, "pic.png")
			if err != nil {
				t.Fatalf("ByName() error = %v", err)
			}
			if got.Sha256 != "sha-one" {
				t.Errorf("sha256 = %q, want sha-one", got.Sha256)
			}
			if err := media.Upsert(ctx, deckA, "pic.png", "sha-two"); err != nil {
				t.Fatalf("second Upsert() error = %v", err)
			}
			got, err = media.ByName(ctx, deckA, "pic.png")
			if err != nil {
				t.Fatalf("ByName() after upsert error = %v", err)
			}
			if got.Sha256 != "sha-two" {
				t.Errorf("sha256 after upsert = %q, want sha-two", got.Sha256)
			}
			var rows int64
			if err := db.Model(&MediaName{}).Where("deck_id = ? AND name = ?", deckA, "pic.png").Count(&rows).Error; err != nil {
				t.Fatalf("count rows: %v", err)
			}
			if rows != 1 {
				t.Errorf("(deck_id, name) has %d rows, want 1 (upsert must not duplicate)", rows)
			}

			// The same name in a different deck is a different file and must be accepted.
			if err := media.Upsert(ctx, deckB, "pic.png", "sha-b"); err != nil {
				t.Fatalf("Upsert(same name, other deck) error = %v", err)
			}
			if other, err := media.ByName(ctx, deckB, "pic.png"); err != nil || other.Sha256 != "sha-b" {
				t.Errorf("ByName(other deck) = %+v, %v; want sha-b", other, err)
			}

			// The schema rejects a second row for the same pair even when written directly.
			dup := MediaName{DeckID: deckA, Name: "pic.png", Sha256: "sha-direct"}
			if err := db.Create(&dup).Error; err == nil {
				t.Error("direct duplicate (deck_id, name) insert was accepted, want a unique violation")
			}

			// NFC equivalence: a composed name is stored, a decomposed lookup finds it, and the reverse upsert does not add a row.
			const composed = "caf\u00e9.png"    // café.png
			const decomposed = "cafe\u0301.png" // cafe + combining acute
			if composed == decomposed {
				t.Fatal("test fixtures are identical; the NFC case would prove nothing")
			}
			if err := media.Upsert(ctx, deckA, composed, "sha-composed"); err != nil {
				t.Fatalf("Upsert(NFC name) error = %v", err)
			}
			found, err := media.ByName(ctx, deckA, decomposed)
			if err != nil {
				t.Fatalf("ByName(decomposed) error = %v, want the composed row", err)
			}
			if found.Name != composed || found.Sha256 != "sha-composed" {
				t.Errorf("ByName(decomposed) = %+v, want name %q sha-composed", found, composed)
			}
			if err := media.Upsert(ctx, deckA, decomposed, "sha-decomposed"); err != nil {
				t.Fatalf("Upsert(decomposed) error = %v", err)
			}
			var nfcRows int64
			if err := db.Model(&MediaName{}).Where("deck_id = ? AND name = ?", deckA, composed).Count(&nfcRows).Error; err != nil {
				t.Fatalf("count NFC rows: %v", err)
			}
			if nfcRows != 1 {
				t.Errorf("NFC rows for %q = %d, want 1 (both spellings must be one row)", composed, nfcRows)
			}

			// List by deck: only this deck's names, ordered by name.
			list, err := media.ListByDeck(ctx, deckA)
			if err != nil {
				t.Fatalf("ListByDeck() error = %v", err)
			}
			var names []string
			for _, row := range list {
				names = append(names, row.Name)
			}
			if len(names) != 2 || names[0] != composed || names[1] != "pic.png" {
				t.Errorf("ListByDeck(deck A) = %v, want [%q pic.png]", names, composed)
			}

			// A name that was never registered is not found.
			if _, err := media.ByName(ctx, deckA, "absent.png"); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByName(absent) error = %v, want gorm.ErrRecordNotFound", err)
			}

			// A deck and a name are both required.
			if err := media.Upsert(ctx, 0, "pic.png", "sha"); err == nil {
				t.Error("Upsert(deck 0) error = nil, want an error")
			}
			if err := media.Upsert(ctx, deckA, "", "sha"); err == nil {
				t.Error("Upsert(empty name) error = nil, want an error")
			}
		})
	}
}

// TestMediaNoteTypeStoreReplace covers the note type media reference set: replace writes normalised unique names, a second
// replace drops names that are no longer referenced, and two note types keep separate sets.
func TestMediaNoteTypeStoreReplace(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "media_note_type_owner")[0]
			deckID := seedDeck(t, db, owner)
			first := seedNoteType(t, db, deckID, "First")
			second := seedNoteType(t, db, deckID, "Second")
			media := NewMediaNoteTypeStore(db)

			const composed = "caf\u00e9.css"
			const decomposed = "cafe\u0301.css"
			if err := media.Replace(ctx, first.ID, []string{"b.js", "a.css", "a.css", decomposed, "", composed}); err != nil {
				t.Fatalf("Replace() error = %v", err)
			}
			names, err := media.List(ctx, first.ID)
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			want := []string{"a.css", "b.js", composed}
			if len(names) != len(want) {
				t.Fatalf("List() = %v, want %v", names, want)
			}
			for i := range want {
				if names[i] != want[i] {
					t.Fatalf("List() = %v, want %v", names, want)
				}
			}

			// Replace is a whole-set write: the names absent from the new set are gone.
			if err := media.Replace(ctx, first.ID, []string{"z.css"}); err != nil {
				t.Fatalf("second Replace() error = %v", err)
			}
			names, err = media.List(ctx, first.ID)
			if err != nil {
				t.Fatalf("List() after replace error = %v", err)
			}
			if len(names) != 1 || names[0] != "z.css" {
				t.Errorf("List() after replace = %v, want [z.css]", names)
			}

			// Sets are per note type.
			if err := media.Replace(ctx, second.ID, []string{"other.png"}); err != nil {
				t.Fatalf("Replace(second) error = %v", err)
			}
			secondNames, err := media.List(ctx, second.ID)
			if err != nil {
				t.Fatalf("List(second) error = %v", err)
			}
			if len(secondNames) != 1 || secondNames[0] != "other.png" {
				t.Errorf("List(second) = %v, want [other.png]", secondNames)
			}

			// An empty set clears the rows.
			if err := media.Replace(ctx, first.ID, nil); err != nil {
				t.Fatalf("Replace(empty) error = %v", err)
			}
			empty, err := media.List(ctx, first.ID)
			if err != nil {
				t.Fatalf("List() after clearing error = %v", err)
			}
			if len(empty) != 0 {
				t.Errorf("List() after clearing = %v, want empty", empty)
			}

			// A note type id is required.
			if err := media.Replace(ctx, 0, []string{"x.css"}); err == nil {
				t.Error("Replace(note type 0) error = nil, want an error")
			}
		})
	}
}
