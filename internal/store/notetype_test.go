package store

import (
	"context"
	"errors"
	"strings"
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
			if _, err := types.Update(ctx, &update, newCfg, NoteTypeUpdateOptions{ExpectedVersion: 0}); err != nil {
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
			if _, err := types.Update(ctx, &stale, noteTypeConfig("Card 3"), NoteTypeUpdateOptions{ExpectedVersion: 0}); !errors.Is(err, ErrVersionConflict) {
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
			if _, err := types.Update(ctx, &missing, noteTypeConfig("Card 1"), NoteTypeUpdateOptions{ExpectedVersion: 0}); !errors.Is(err, gorm.ErrRecordNotFound) {
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

// noteTypeAnswerConfig is noteTypeConfig with an answer spec that binds a role to a declared field.
func noteTypeAnswerConfig() notetype.Config {
	cfg := noteTypeConfig("Card 1")
	cfg.Templates[0].QFormat = "{{Front}}{{type:Back}}"
	cfg.Templates[0].Answer = &notetype.AnswerSpec{
		Control:  "numeric",
		Bindings: map[string]string{"value": "f-back"},
	}
	return cfg
}

// TestNoteTypeSetConfigValidatesAnswerSpec proves SetConfig rejects an answer spec whose control is unknown, whose
// required role is unbound, or whose bound field id the config does not declare. The error names the template and the
// role (or the control, when no role can be named), and a rejected config writes nothing.
func TestNoteTypeSetConfigValidatesAnswerSpec(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*notetype.Config)
		want   []string
	}{
		{
			name:   "unknown control",
			mutate: func(cfg *notetype.Config) { cfg.Templates[0].Answer.Control = "does_not_exist" },
			want:   []string{`template "Card 1"`, `"does_not_exist"`},
		},
		{
			name:   "missing required role",
			mutate: func(cfg *notetype.Config) { cfg.Templates[0].Answer = &notetype.AnswerSpec{Control: "numeric"} },
			want:   []string{`template "Card 1"`, `role "value"`},
		},
		{
			name:   "role bound to an undeclared field id",
			mutate: func(cfg *notetype.Config) { cfg.Templates[0].Answer.Bindings["value"] = "f-missing" },
			want:   []string{`template "Card 1"`, `role "value"`, `"f-missing"`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := noteTypeAnswerConfig()
			test.mutate(&cfg)
			var nt NoteType
			err := nt.SetConfig("Answer type", cfg)
			if err == nil {
				t.Fatal("SetConfig() = nil, want a rejected answer spec")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("SetConfig() error = %v, want substring %q", err, want)
				}
			}
			if nt.Name != "" || nt.ConfigJSON != "" {
				t.Errorf("rejected config wrote name %q and config %q, want both empty", nt.Name, nt.ConfigJSON)
			}
		})
	}

	var valid NoteType
	if err := valid.SetConfig("Answer type", noteTypeAnswerConfig()); err != nil {
		t.Fatalf("SetConfig(valid answer spec) = %v, want nil", err)
	}
}

// noteTypeUpdateConfig builds a normal note type config with the given templates. Every case leaves the
// "Extra" field empty, so a template that renders only that field generates no card for the note.
func noteTypeUpdateConfig(templates ...notetype.Template) notetype.Config {
	return notetype.Config{
		Kind: notetype.KindNormal,
		Fields: []notetype.Field{
			{ID: "f-front", Name: "Front"},
			{ID: "f-back", Name: "Back"},
			{ID: "f-extra", Name: "Extra"},
		},
		Templates:   templates,
		CSS:         ".card { color: black; }",
		SortFieldID: "f-front",
	}
}

// TestNoteTypeUpdateRemovalFollowsDeclaredTemplates covers which cards a note type update removes:
//   - a card survives when its template id is still declared, even if the template now renders an
//     empty front for the note, and the update reports cards_removed = 0;
//   - a card is soft-deleted when its template id is no longer declared;
//   - a cloze update removes no card;
//   - a card of a declared template is restored when the new config generates it again.
func TestNoteTypeUpdateRemovalFollowsDeclaredTemplates(t *testing.T) {
	front := notetype.Template{ID: "t-front", Name: "Front", QFormat: "{{Front}}", AFormat: "{{Back}}"}
	extra := notetype.Template{ID: "t-extra", Name: "Extra", QFormat: "{{Extra}}", AFormat: "{{Back}}"}
	emptyFront := front
	emptyFront.QFormat = "{{Extra}}"
	frontAnswer := front
	frontAnswer.AFormat = "{{Back}} (answer)"

	clozeConfig := func() notetype.Config {
		cfg := noteTypeUpdateConfig()
		cfg.Kind = notetype.KindCloze
		cfg.Fields = []notetype.Field{{ID: "f-front", Name: "Front"}, {ID: "f-back", Name: "Back"}}
		cfg.Templates = []notetype.Template{
			{ID: "t-cloze", Name: "Cloze", QFormat: "{{cloze:Front}}", AFormat: "{{Front}}"},
		}
		return cfg
	}

	cases := []struct {
		name        string
		initial     notetype.Config
		fields      map[string]any
		prepare     func(t *testing.T, db *gorm.DB, noteID uint64)
		updated     notetype.Config
		wantAdded   int
		wantRemoved int
		wantVisible []string
		wantDeleted []string
	}{
		{
			name:        "declared template with an empty front keeps its card",
			initial:     noteTypeUpdateConfig(front),
			fields:      map[string]any{"Front": "Q1", "Back": "A1", "Extra": ""},
			updated:     noteTypeUpdateConfig(emptyFront),
			wantAdded:   0,
			wantRemoved: 0,
			wantVisible: []string{"t-front"},
		},
		{
			name:        "undeclared template removes its card",
			initial:     noteTypeUpdateConfig(front, extra),
			fields:      map[string]any{"Front": "Q1", "Back": "A1", "Extra": "E1"},
			updated:     noteTypeUpdateConfig(front),
			wantAdded:   0,
			wantRemoved: 1,
			wantVisible: []string{"t-front"},
			wantDeleted: []string{"t-extra"},
		},
		{
			name:    "cloze update removes no card",
			initial: clozeConfig(),
			fields:  map[string]any{"Front": "{{c1::one}} {{c2::two}}", "Back": ""},
			prepare: func(t *testing.T, db *gorm.DB, noteID uint64) {
				t.Helper()
				// A card the compacted fields no longer produce: the keep rule must leave it alone.
				stale := Card{NoteID: noteID, Template: "cloze:9", Ordinal: 8, CreatedAt: time.Now().UTC()}
				if err := db.Create(&stale).Error; err != nil {
					t.Fatalf("insert a stale cloze card: %v", err)
				}
			},
			updated:     clozeConfig(),
			wantAdded:   0,
			wantRemoved: 0,
			wantVisible: []string{"cloze:1", "cloze:2", "cloze:9"},
		},
		{
			name:    "declared template restores its soft-deleted card",
			initial: noteTypeUpdateConfig(front),
			fields:  map[string]any{"Front": "Q1", "Back": "A1"},
			prepare: func(t *testing.T, db *gorm.DB, noteID uint64) {
				t.Helper()
				if err := db.Model(&Card{}).Where("note_id = ?", noteID).Update("deleted_at", time.Now().UTC()).Error; err != nil {
					t.Fatalf("soft-delete the note's cards: %v", err)
				}
			},
			updated:     noteTypeUpdateConfig(frontAnswer),
			wantAdded:   1,
			wantRemoved: 0,
			wantVisible: []string{"t-front"},
		},
	}

	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_removal_owner")[0]
			deckID := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)
			notes := NewNoteStore(db)

			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					nt := NoteType{DeckID: deckID, Name: test.name}
					if err := types.Create(ctx, &nt, test.initial); err != nil {
						t.Fatalf("create note type: %v", err)
					}
					note := Note{DeckID: deckID, NoteTypeID: &nt.ID}
					if _, err := notes.Create(ctx, &note, test.fields); err != nil {
						t.Fatalf("create note: %v", err)
					}
					if test.prepare != nil {
						test.prepare(t, db, note.ID)
					}

					update := nt
					impact, err := types.Update(ctx, &update, test.updated,
						NoteTypeUpdateOptions{ExpectedVersion: 0, ConfirmCardRemoval: true})
					if err != nil {
						t.Fatalf("Update() error = %v", err)
					}
					if impact.CardsAdded != test.wantAdded || impact.CardsRemoved != test.wantRemoved {
						t.Errorf("impact = added %d removed %d, want %d/%d",
							impact.CardsAdded, impact.CardsRemoved, test.wantAdded, test.wantRemoved)
					}

					var cards []Card
					if err := db.Unscoped().Where("note_id = ?", note.ID).Find(&cards).Error; err != nil {
						t.Fatalf("load cards: %v", err)
					}
					if len(cards) != len(test.wantVisible)+len(test.wantDeleted) {
						t.Fatalf("stored cards = %d, want %d", len(cards), len(test.wantVisible)+len(test.wantDeleted))
					}
					visible := make(map[string]bool, len(cards))
					for _, card := range cards {
						visible[card.Template] = !card.DeletedAt.Valid
					}
					for _, template := range test.wantVisible {
						if got, ok := visible[template]; !ok || !got {
							t.Errorf("card %q is not visible after the update (present %v)", template, ok)
						}
					}
					for _, template := range test.wantDeleted {
						if got, ok := visible[template]; !ok || got {
							t.Errorf("card %q is not soft-deleted after the update (present %v)", template, ok)
						}
					}
				})
			}
		})
	}
}

// TestNoteTypeDelete covers the refused delete. A live note blocks it outright; a type whose notes are all
// soft-deleted is refused until the caller confirms the purge; a type with no note at all is deletable.
func TestNoteTypeDelete(t *testing.T) {
	cases := []struct {
		name string
		// noteState is how the type's single note is stored: "live", "soft-deleted", or "" for a type with no note at all.
		noteState string
		wantGone  bool
		wantErr   error
	}{
		{name: "live note blocks the delete", noteState: "live", wantErr: ErrNoteTypeInUse},
		{name: "soft-deleted note needs purge confirmation", noteState: "soft-deleted", wantErr: ErrNoteTypePurgeUnconfirmed},
		{name: "no note allows the delete", wantGone: true},
	}

	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_delete_owner")[0]
			deckID := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)

			var deletedID uint64
			for _, test := range cases {
				t.Run(test.name, func(t *testing.T) {
					nt := seedNoteType(t, db, deckID, test.name)
					if test.noteState != "" {
						now := time.Now().UTC()
						note := Note{DeckID: deckID, NoteTypeID: &nt.ID,
							FieldsJSON: `{"f-front":"q","f-back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
						if err := db.Create(&note).Error; err != nil {
							t.Fatalf("create note: %v", err)
						}
						if test.noteState == "soft-deleted" {
							if err := db.Delete(&Note{}, note.ID).Error; err != nil {
								t.Fatalf("soft delete note: %v", err)
							}
							// The fixture is only meaningful if the note really left the default scope: that is the
							// state whose restore must not be orphaned by this delete.
							var live int64
							if err := db.Model(&Note{}).Where("id = ?", note.ID).Count(&live).Error; err != nil {
								t.Fatalf("count visible notes: %v", err)
							}
							if live != 0 {
								t.Fatalf("note still visible after soft delete: %d row(s)", live)
							}
						}
					}

					_, err := types.Delete(ctx, nt.ID, NoteTypeDeleteOptions{})
					if test.wantGone {
						if err != nil {
							t.Fatalf("Delete(no note) error = %v, want nil", err)
						}
					} else if !errors.Is(err, test.wantErr) {
						t.Fatalf("Delete() error = %v, want %v", err, test.wantErr)
					}

					// A refused delete leaves the type in place; a permitted one removes it.
					_, getErr := types.ByID(ctx, nt.ID)
					if test.wantGone {
						deletedID = nt.ID
						if !errors.Is(getErr, gorm.ErrRecordNotFound) {
							t.Errorf("ByID() after delete error = %v, want gorm.ErrRecordNotFound", getErr)
						}
					} else if getErr != nil {
						t.Fatalf("note type missing after refused delete: %v", getErr)
					}
				})
			}

			// Deleting an absent type reports not found.
			if _, err := types.Delete(ctx, deletedID, NoteTypeDeleteOptions{}); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("Delete(absent) error = %v, want gorm.ErrRecordNotFound", err)
			}
		})
	}
}

// TestNoteTypeDeletePurgesSoftDeletedNotes proves a confirmed purge removes the soft-deleted notes of the type
// with their cards, card_states and media_notes rows, keeps the review log, and that the unconfirmed call
// writes nothing while reporting the same counts.
func TestNoteTypeDeletePurgesSoftDeletedNotes(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "note_type_purge_owner")[0]
			deckID := seedDeck(t, db, owner)
			types := NewNoteTypeStore(db)
			nt := seedNoteType(t, db, deckID, "Purge me")

			now := time.Now().UTC()
			note := Note{DeckID: deckID, NoteTypeID: &nt.ID,
				FieldsJSON: `{"f-front":"q","f-back":"a"}`, TagsJSON: "[]", CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&note).Error; err != nil {
				t.Fatalf("create note: %v", err)
			}
			live := Card{NoteID: note.ID, Template: "t-card", CreatedAt: now}
			if err := db.Create(&live).Error; err != nil {
				t.Fatalf("create card: %v", err)
			}
			removed := Card{NoteID: note.ID, Template: "t-old", CreatedAt: now}
			if err := db.Create(&removed).Error; err != nil {
				t.Fatalf("create soft-deleted card: %v", err)
			}
			// A card a template change soft-deleted is still a row the purge removes; it counts too.
			if err := db.Delete(&Card{}, removed.ID).Error; err != nil {
				t.Fatalf("soft delete card: %v", err)
			}
			seedProgress(t, db, owner, live.ID)
			if err := db.Create(&MediaNote{MediaSha: strings.Repeat("a", 64), NoteID: note.ID, CreatedAt: now}).Error; err != nil {
				t.Fatalf("create media note: %v", err)
			}
			if err := db.Delete(&Note{}, note.ID).Error; err != nil {
				t.Fatalf("soft delete note: %v", err)
			}

			want := NoteTypePurgeImpact{DeletedNotes: 1, Cards: 2, Reviews: 1}
			impact, err := types.Delete(ctx, nt.ID, NoteTypeDeleteOptions{})
			if !errors.Is(err, ErrNoteTypePurgeUnconfirmed) {
				t.Fatalf("Delete(unconfirmed) error = %v, want ErrNoteTypePurgeUnconfirmed", err)
			}
			if impact != want {
				t.Errorf("Delete(unconfirmed) impact = %+v, want %+v", impact, want)
			}

			// The unconfirmed call writes nothing: the type, the soft-deleted note and every dependent row stay.
			if _, err := types.ByID(ctx, nt.ID); err != nil {
				t.Errorf("note type missing after unconfirmed delete: %v", err)
			}
			assertNoteTypePurgeRows(t, db, note.ID, nt.ID, []uint64{live.ID, removed.ID}, 1, 2, 1, 1, 1)

			impact, err = types.Delete(ctx, nt.ID, NoteTypeDeleteOptions{ConfirmPurge: true})
			if err != nil {
				t.Fatalf("Delete(confirmed) error = %v, want nil", err)
			}
			if impact != want {
				t.Errorf("Delete(confirmed) impact = %+v, want %+v", impact, want)
			}
			if _, err := types.ByID(ctx, nt.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Errorf("ByID() after purge error = %v, want gorm.ErrRecordNotFound", err)
			}
			// Notes, cards, card_states and media_notes are gone; the review row survives the purge.
			assertNoteTypePurgeRows(t, db, note.ID, nt.ID, []uint64{live.ID, removed.ID}, 0, 0, 0, 0, 1)
		})
	}
}

// assertNoteTypePurgeRows checks the row counts a note type purge leaves behind. Notes and cards are counted
// unscoped, because the purge removes soft-deleted rows too. cardStates and reviews are counted by the card ids
// captured before the purge, so a deleted card row cannot hide a surviving progress row.
func assertNoteTypePurgeRows(t *testing.T, db *gorm.DB, noteID, noteTypeID uint64, cardIDs []uint64, wantNotes, wantCards, wantStates, wantMedia, wantReviews int64) {
	t.Helper()
	counts := []struct {
		name  string
		query *gorm.DB
		want  int64
	}{
		{"notes", db.Unscoped().Model(&Note{}).Where("note_type_id = ?", noteTypeID), wantNotes},
		{"cards", db.Unscoped().Model(&Card{}).Where("note_id = ?", noteID), wantCards},
		{"card_states", db.Model(&CardState{}).Where("card_id IN ?", cardIDs), wantStates},
		{"media_notes", db.Model(&MediaNote{}).Where("note_id = ?", noteID), wantMedia},
		{"reviews", db.Model(&Review{}).Where("card_id IN ?", cardIDs), wantReviews},
	}
	for _, c := range counts {
		var got int64
		if err := c.query.Count(&got).Error; err != nil {
			t.Fatalf("count %s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s after delete = %d, want %d", c.name, got, c.want)
		}
	}
}

func noteTypeNames(types []NoteType) []string {
	out := make([]string, 0, len(types))
	for _, nt := range types {
		out = append(out, nt.Name)
	}
	return out
}
