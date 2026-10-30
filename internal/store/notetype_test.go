package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// noteTypeConfig builds a valid normal note type config for the tests; templateName lets a case vary the template.
func noteTypeConfig(templateName string) notetype.Config {
	return notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back"},
		},
		Templates: []notetype.Template{
			{ID: "t-card", Name: templateName, QFormat: "{{Front}}", AFormat: "{{Back}}"},
		},
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
}

// seedNoteType creates a note type through the store and returns its row.
func seedNoteType(t *testing.T, db *gorm.DB, deckID uint64, name string) NoteType {
	t.Helper()
	nt := NoteType{DeckID: deckID, Name: name}
	if err := NewNoteTypeStore(db).Create(context.Background(), &nt, noteTypeConfig("Card 1")); err != nil {
		t.Fatalf("create note type %q: %v", name, err)
	}
	return nt
}

// TestNoteTypeCreateGetListUpdate covers the CRUD surface and the optimistic lock: create/get/public id, listing a deck
// ordered by name, the config round trip, a successful compare-and-swap and the conflict it must reject.
func TestNoteTypeCreateGetListUpdate(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_owner")[0]
			deckA := seedDeck(t, db, owner)
			deckB := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)

			beta := seedNoteType(t, db, deckA, "Beta")
			alpha := seedNoteType(t, db, deckA, "Alpha")
			gamma := seedNoteType(t, db, deckB, "Gamma")

			// Create: the public id is generated and the version starts at 0.
			if beta.PublicID == "" || alpha.PublicID == "" || gamma.PublicID == "" {
				t.Fatal("Create() did not populate PublicID")
			}
			if beta.PublicID == alpha.PublicID {
				t.Errorf("two note types share public id %q", beta.PublicID)
			}
			if beta.Version != 0 {
				t.Errorf("new note type version = %d, want 0", beta.Version)
			}

			// Get by id and by public id return the same row.
			byID, err := types.ByID(ctx, beta.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			byPublic, err := types.ByPublicID(ctx, beta.PublicID)
			if err != nil {
				t.Fatalf("ByPublicID() error = %v", err)
			}
			if byID.ID != byPublic.ID || byID.Name != "Beta" {
				t.Errorf("ByID() = %+v, ByPublicID() = %+v, want the same Beta row", byID, byPublic)
			}
			if _, err := types.ByPublicID(ctx, ""); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByPublicID(\"\") error = %v, want gorm.ErrRecordNotFound", err)
			}

			// List by deck: only that deck's types, ordered by name then id.
			list, err := types.ListByDeck(ctx, deckA)
			if err != nil {
				t.Fatalf("ListByDeck() error = %v", err)
			}
			if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Beta" {
				t.Fatalf("ListByDeck(deck A) = %v, want [Alpha Beta]", noteTypeNames(list))
			}
			other, err := types.ListByDeck(ctx, deckB)
			if err != nil {
				t.Fatalf("ListByDeck(deck B) error = %v", err)
			}
			if len(other) != 1 || other[0].Name != "Gamma" {
				t.Errorf("ListByDeck(deck B) = %v, want [Gamma]", noteTypeNames(other))
			}

			// The config survives the JSON TEXT column unchanged.
			stored, err := types.ByID(ctx, beta.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			cfg, err := stored.Config()
			if err != nil {
				t.Fatalf("Config() error = %v", err)
			}
			if cfg.Kind != notetype.KindNormal || len(cfg.Fields) != 2 || cfg.Fields[0].ID != "f-front" ||
				len(cfg.Templates) != 1 || cfg.Templates[0].QFormat != "{{Front}}" || cfg.CSS == "" || cfg.SortFieldID != "f-front" {
				t.Errorf("config round trip mismatch: %+v", cfg)
			}

			// Update with the version that was read: succeeds and increments the version.
			update := *stored
			newCfg := noteTypeConfig("Card 2")
			newCfg.Templates[0].QFormat = "{{Back}}"
			newCfg.Templates[0].AFormat = "{{Front}}"
			if err := types.Update(ctx, &update, newCfg, 0); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if update.Version != 1 {
				t.Errorf("version after update = %d, want 1", update.Version)
			}
			reloaded, err := types.ByID(ctx, beta.ID)
			if err != nil {
				t.Fatalf("ByID() after update error = %v", err)
			}
			if reloaded.Name != "Beta" || reloaded.Version != 1 {
				t.Errorf("after update = name %q version %d, want Beta and 1", reloaded.Name, reloaded.Version)
			}
			reloadedCfg, err := reloaded.Config()
			if err != nil {
				t.Fatalf("Config() after update error = %v", err)
			}
			if reloadedCfg.Templates[0].Name != "Card 2" || reloadedCfg.Templates[0].QFormat != "{{Back}}" {
				t.Errorf("config after update = %+v, want the replaced template", reloadedCfg)
			}

			// A stale version is rejected with ErrVersionConflict and changes nothing.
			stale := *reloaded
			stale.Name = "Should Not Land"
			if err := types.Update(ctx, &stale, noteTypeConfig("Card 3"), 0); !errors.Is(err, ErrVersionConflict) {
				t.Fatalf("stale Update() error = %v, want ErrVersionConflict", err)
			}
			afterConflict, err := types.ByID(ctx, beta.ID)
			if err != nil {
				t.Fatalf("ByID() after conflict error = %v", err)
			}
			if afterConflict.Name != "Beta" || afterConflict.Version != 1 {
				t.Errorf("row changed by a rejected update: name %q version %d", afterConflict.Name, afterConflict.Version)
			}

			// Updating a row that does not exist reports not found rather than a conflict.
			missing := NoteType{ID: 999999, Name: "Missing"}
			if err := types.Update(ctx, &missing, noteTypeConfig("Card 1"), 0); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("Update(absent) error = %v, want gorm.ErrRecordNotFound", err)
			}
		})
	}
}

// TestNoteTypeValidation proves an illegal name or config is rejected before the write and names the offending item.
func TestNoteTypeValidation(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_validation_owner")[0]
			deckID := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)

			emptyName := NoteType{DeckID: deckID, Name: "  "}
			err := types.Create(ctx, &emptyName, noteTypeConfig("Card 1"))
			var verr *notetype.ValidationError
			if !errors.As(err, &verr) || verr.Rule != notetype.RuleEmptyNoteTypeName {
				t.Errorf("Create(empty name) error = %v, want ValidationError rule %q", err, notetype.RuleEmptyNoteTypeName)
			}

			dupField := noteTypeConfig("Card 1")
			dupField.Fields[1].ID = dupField.Fields[0].ID
			dup := NoteType{DeckID: deckID, Name: "Dup"}
			err = types.Create(ctx, &dup, dupField)
			if !errors.As(err, &verr) || verr.Rule != notetype.RuleDuplicateFieldID {
				t.Fatalf("Create(duplicate field id) error = %v, want ValidationError rule %q", err, notetype.RuleDuplicateFieldID)
			}
			if verr.Detail == "" {
				t.Errorf("validation detail is empty, want the offending field named")
			}

			unknownField := noteTypeConfig("Card 1")
			unknownField.Templates[0].QFormat = "{{Front}} {{Missing}}"
			bad := NoteType{DeckID: deckID, Name: "Bad Template"}
			err = types.Create(ctx, &bad, unknownField)
			if !errors.As(err, &verr) || verr.Rule != notetype.RuleUnknownField {
				t.Errorf("Create(unknown template field) error = %v, want ValidationError rule %q", err, notetype.RuleUnknownField)
			}

			list, err := types.ListByDeck(ctx, deckID)
			if err != nil {
				t.Fatalf("ListByDeck() error = %v", err)
			}
			if len(list) != 0 {
				t.Errorf("rejected creates left %d row(s), want 0", len(list))
			}
		})
	}
}

// TestNoteTypeDelete covers the refused delete: a note that is not soft-deleted blocks it, a soft-deleted note does not,
// and the type is gone once no live note references it.
func TestNoteTypeDelete(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_delete_owner")[0]
			deckID := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)
			nt := seedNoteType(t, db, deckID, "Used Type")

			now := time.Now().UTC()
			note := Note{DeckID: deckID, NoteTypeID: &nt.ID,
				FieldsJSON: `{"f-front":"q","f-back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&note).Error; err != nil {
				t.Fatalf("create note: %v", err)
			}

			// A live note blocks the delete with the typed sentinel.
			if err := types.Delete(ctx, nt.ID); !errors.Is(err, ErrNoteTypeInUse) {
				t.Fatalf("Delete() error = %v, want ErrNoteTypeInUse", err)
			}
			if _, err := types.ByID(ctx, nt.ID); err != nil {
				t.Fatalf("note type missing after refused delete: %v", err)
			}

			// A type that no note uses is deletable. A note without a type cannot exist, because the
			// column is NOT NULL, so there is no second "unused" case to cover.
			unused := seedNoteType(t, db, deckID, "Unused Type")
			if err := types.Delete(ctx, unused.ID); err != nil {
				t.Errorf("Delete(unused) error = %v, want nil", err)
			}

			// Soft-deleting the note releases the type: a deleted note no longer counts as content.
			if err := db.Delete(&Note{}, note.ID).Error; err != nil {
				t.Fatalf("soft delete note: %v", err)
			}
			if err := types.Delete(ctx, nt.ID); err != nil {
				t.Fatalf("Delete() after note soft delete error = %v, want nil", err)
			}
			if _, err := types.ByID(ctx, nt.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByID() after delete error = %v, want gorm.ErrRecordNotFound", err)
			}

			// Deleting an absent type reports not found.
			if err := types.Delete(ctx, nt.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("Delete(absent) error = %v, want gorm.ErrRecordNotFound", err)
			}
		})
	}
}

func noteTypeNames(types []NoteType) []string {
	out := make([]string, 0, len(types))
	for _, nt := range types {
		out = append(out, nt.Name)
	}
	return out
}
