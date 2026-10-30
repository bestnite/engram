package store

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// backfillNoteTypeMedia fills media_note_types for every existing note type from its stored config.
//
// Before this migration, a note type's `_`-prefixed assets were invisible to garbage collection: only note fields produced
// media_notes rows. A row written before the change would still miss its reference after upgrading, so this scans every
// note type and replaces its derived set. Replace is idempotent (delete-then-insert), so running it again changes nothing.
func backfillNoteTypeMedia(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("media_note_types") || !tx.Migrator().HasTable("note_types") {
		return nil
	}
	var types []NoteType
	if err := tx.Select("id", "config_json").Order("id ASC").Find(&types).Error; err != nil {
		return fmt.Errorf("backfill note type media: scan note types: %w", err)
	}
	ctx := context.Background()
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			// A corrupt config cannot name a reference; leaving its set empty is the only truthful result.
			continue
		}
		if err := NewMediaNoteTypeStore(tx).Replace(ctx, types[i].ID, noteTypeMediaNames(cfg)); err != nil {
			return fmt.Errorf("backfill note type media: note type %d: %w", types[i].ID, err)
		}
	}
	return nil
}
