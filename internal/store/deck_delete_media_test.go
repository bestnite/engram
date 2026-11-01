package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDeckDeleteReleasesNoteTypeMedia is the acceptance for the deck-deletion residue: a deck's note types and
// media names are part of the deck, so deleting it must remove them too. Otherwise the template reference keeps
// the collector convinced the file is in use and the bytes are never reclaimed.
func TestDeckDeleteReleasesNoteTypeMedia(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			if err := AutoMigrate(ctx, db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			now := time.Now().UTC()
			owner := seedUsers(t, db, "deck_delete_media_owner")[0]
			deckID := seedDeck(t, db, owner)

			cfg := cleanupConfig()
			cfg.CSS = `@font-face { font-family: deck; src: url(_font.woff2); }`
			nt := createType(t, db, deckID, "Deck assets", cfg)

			sha := strings.Repeat("a", 64)
			if err := db.Create(&Media{Sha256: sha, RelPath: "aa/" + sha + ".woff2", Mime: "font/woff2", Bytes: 1000, CreatedAt: now}).Error; err != nil {
				t.Fatalf("create media: %v", err)
			}
			if err := RecordMediaUploader(ctx, db, sha, owner); err != nil {
				t.Fatalf("record uploader: %v", err)
			}
			if err := NewMediaNameStore(db).Upsert(ctx, deckID, "_font.woff2", sha); err != nil {
				t.Fatalf("register media name: %v", err)
			}

			// Fixture sanity: the upload is charged to its uploader and the template reference is recorded.
			usage, err := UserMediaUsage(ctx, db, owner)
			if err != nil {
				t.Fatalf("UserMediaUsage() error = %v", err)
			}
			if usage.Bytes != 1000 {
				t.Fatalf("usage before delete = %d, want 1000", usage.Bytes)
			}
			var refs int64
			if err := db.Model(&MediaNoteType{}).Where("note_type_id = ?", nt.ID).Count(&refs).Error; err != nil {
				t.Fatalf("count note type media refs: %v", err)
			}
			if refs != 1 {
				t.Fatalf("note type media refs = %d, want 1 (fixture sanity)", refs)
			}

			if err := NewDeckStore(db).Delete(ctx, owner, deckID); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			residue := []struct {
				model any
				where string
				args  []any
			}{
				{&NoteType{}, "deck_id = ?", []any{deckID}},
				{&MediaName{}, "deck_id = ?", []any{deckID}},
				{&MediaNoteType{}, "note_type_id = ?", []any{nt.ID}},
			}
			for _, row := range residue {
				var n int64
				if err := db.Model(row.model).Where(row.where, row.args...).Count(&n).Error; err != nil {
					t.Fatalf("count residue %T: %v", row.model, err)
				}
				if n != 0 {
					t.Errorf("residue %T = %d rows after deck delete, want 0", row.model, n)
				}
			}

			marked, _, err := MarkOrphanedMedia(ctx, db, now)
			if err != nil {
				t.Fatalf("MarkOrphanedMedia() error = %v", err)
			}
			if marked != 1 {
				t.Fatalf("MarkOrphanedMedia() marked %d rows, want 1: the template asset is now unreferenced", marked)
			}
			deleted, err := DeleteOrphanedMedia(ctx, db, now.Add(time.Hour), 10)
			if err != nil {
				t.Fatalf("DeleteOrphanedMedia() error = %v", err)
			}
			if len(deleted) != 1 || deleted[0].Sha256 != sha {
				t.Fatalf("DeleteOrphanedMedia() = %+v, want the deck's media", deleted)
			}
			usage, err = UserMediaUsage(ctx, db, owner)
			if err != nil {
				t.Fatalf("UserMediaUsage() after collect error = %v", err)
			}
			if usage.Bytes != 0 {
				t.Errorf("usage after collect = %d, want 0: the deleted deck must release its media", usage.Bytes)
			}
		})
	}
}
