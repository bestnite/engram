package store

import (
	"context"
	"reflect"
	"testing"
)

// TestBackfillNoteTypeMedia is the migration acceptance: a note type written before media_note_types existed gains its
// `_`-prefixed reference set from the stored config, and running the migration again is a no-op.
func TestBackfillNoteTypeMedia(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := AutoMigrate(context.Background(), db); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			cfg := noteTypeConfig("Card 1")
			cfg.CSS = `@font-face { src: url(_font.woff2); }`
			nt := NoteType{DeckID: 7, Name: "Fonts"}
			if err := nt.SetConfig("Fonts", cfg); err != nil {
				t.Fatalf("SetConfig: %v", err)
			}
			// Insert raw, as a pre-change row would have been: Create would already fill media_note_types.
			if err := db.Create(&nt).Error; err != nil {
				t.Fatalf("insert note type: %v", err)
			}
			if err := backfillNoteTypeMedia(db); err != nil {
				t.Fatalf("backfill: %v", err)
			}
			got, err := NewMediaNoteTypeStore(db).List(context.Background(), nt.ID)
			if err != nil {
				t.Fatalf("list media: %v", err)
			}
			if want := []string{"_font.woff2"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("media references = %v, want %v", got, want)
			}
			// Idempotent: a second run produces the same set.
			if err := backfillNoteTypeMedia(db); err != nil {
				t.Fatalf("second backfill: %v", err)
			}
			again, err := NewMediaNoteTypeStore(db).List(context.Background(), nt.ID)
			if err != nil {
				t.Fatalf("list media after second run: %v", err)
			}
			if !reflect.DeepEqual(again, got) {
				t.Fatalf("second run changed the set: %v != %v", again, got)
			}

			// A config that dropped the reference clears it on the next run.
			cfg.CSS = ".card { color: black; }"
			nt.ConfigJSON = ""
			if err := nt.SetConfig("Fonts", cfg); err != nil {
				t.Fatalf("SetConfig: %v", err)
			}
			if err := db.Model(&NoteType{}).Where("id = ?", nt.ID).Update("config_json", nt.ConfigJSON).Error; err != nil {
				t.Fatalf("update config: %v", err)
			}
			if err := backfillNoteTypeMedia(db); err != nil {
				t.Fatalf("third backfill: %v", err)
			}
			cleared, err := NewMediaNoteTypeStore(db).List(context.Background(), nt.ID)
			if err != nil {
				t.Fatalf("list media after clear: %v", err)
			}
			if len(cleared) != 0 {
				t.Fatalf("media references = %v, want none", cleared)
			}
		})
	}
}
