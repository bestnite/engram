package store

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// ensureClozeMetadataField adds the shared metadata field to every stored note type that grades
// cloze blanks.
//
// The field used to be added by an explicit note-type settings action, so a type created or
// imported before the field became built in may lack it while its templates already use
// cloze_blanks. This migration applies the same rule the write path does: append the field when
// absent, normalise its flags when present, and leave every other field, id and value alone.
//
// Only a row whose config actually changes is written, and it is written with a version bump, so
// a second run updates nothing. Cards are not regenerated: the field is unreferenced by every
// template and excluded from card generation, so no card set changes.
func ensureClozeMetadataField(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("note_types") {
		return nil
	}
	var types []NoteType
	if err := tx.Select("id", "config_json").Order("id ASC").Find(&types).Error; err != nil {
		return fmt.Errorf("ensure cloze metadata field: scan note types: %w", err)
	}
	now := time.Now().UTC()
	for i := range types {
		cfg, err := types[i].Config()
		if err != nil {
			// A corrupt config cannot declare a cloze_blanks template, so there is nothing this
			// migration can truthfully add to it; skipping leaves the row exactly as it was.
			continue
		}
		if !notetype.EnsureMetadataField(&cfg) {
			continue
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("ensure cloze metadata field: encode note type %d: %w", types[i].ID, err)
		}
		if err := tx.Model(&NoteType{}).Where("id = ?", types[i].ID).Updates(map[string]any{
			"config_json": string(raw),
			"updated_at":  now,
			"version":     gorm.Expr("version + 1"),
		}).Error; err != nil {
			return fmt.Errorf("ensure cloze metadata field: note type %d: %w", types[i].ID, err)
		}
	}
	return nil
}
