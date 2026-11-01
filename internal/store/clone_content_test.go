package store

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/notetype"
)

// TestCloneCopiesSourceNoteType asserts that a clone keeps the source note type's own config instead of
// rebuilding an engram stock type: an imported type keeps its f0/f1 fields and t0 template, and a
// stock-shaped type keeps its custom CSS. The cloned note must still render after the source deck, and
// with it the source note type, has been deleted.
func TestCloneCopiesSourceNoteType(t *testing.T) {
	importedCSS := ".card { color: rgb(10, 20, 30); } .imported { font-weight: 700; }"
	stock, ok := notetype.EngramStockByKind("basic")
	if !ok {
		t.Fatal("the basic engram stock type is missing")
	}
	customCSS := ".card { color: rgb(7, 8, 9); } .custom { letter-spacing: 1px; }"

	cases := []struct {
		name     string
		typeName string
		cfg      notetype.Config
	}{
		{
			name:     "imported note type",
			typeName: "Imported",
			cfg: notetype.Config{
				Kind: notetype.KindNormal,
				Fields: []notetype.Field{
					{ID: "f0", Name: "Front"},
					{ID: "f1", Name: "Back"},
				},
				Templates: []notetype.Template{
					{ID: "t0", Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{FrontSide}}<hr>{{Back}}"},
				},
				CSS:         importedCSS,
				SortFieldID: "f0",
				AnkiOrigin:  &notetype.AnkiOrigin{NoteTypeID: 1700000000001, MTime: 1700000000},
			},
		},
		{
			name:     "stock type with custom styling",
			typeName: "Styled",
			cfg: func() notetype.Config {
				cfg := stock.Config
				cfg.CSS = customCSS
				return cfg
			}(),
		},
	}

	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					assertCloneCopiesSourceNoteType(t, db, tc.name, tc.typeName, tc.cfg)
				})
			}
		})
	}
}

func assertCloneCopiesSourceNoteType(t *testing.T, db *gorm.DB, name, typeName string, cfg notetype.Config) {
	t.Helper()
	ctx := context.Background()
	owner := seedUsers(t, db, "clone_type_"+name)[0]
	presetID := seedPresetRow(t, db, owner)
	src := Deck{OwnerUserID: owner, Name: "source " + name, PresetID: presetID}
	if err := NewDeckStore(db).Create(ctx, &src); err != nil {
		t.Fatalf("create source deck: %v", err)
	}
	srcType := NoteType{DeckID: src.ID, Name: typeName}
	if err := NewNoteTypeStore(db).Create(ctx, &srcType, cfg); err != nil {
		t.Fatalf("create source note type: %v", err)
	}
	note := Note{DeckID: src.ID, NoteTypeID: Ptr(srcType.ID), CreatedBy: Ptr(owner)}
	fields := map[string]any{cfg.Fields[0].ID: "the question", cfg.Fields[1].ID: "the answer"}
	if _, err := NewNoteStore(db).Save(ctx, &note, fields); err != nil {
		t.Fatalf("create source note: %v", err)
	}

	clone, err := NewDeckStore(db).Clone(ctx, &src, owner, "clone "+name, presetID)
	if err != nil {
		t.Fatalf("Clone() error = %v, want nil", err)
	}

	var clonedNote Note
	if err := db.WithContext(ctx).Where("deck_id = ?", clone.ID).First(&clonedNote).Error; err != nil {
		t.Fatalf("load cloned note: %v", err)
	}
	if clonedNote.NoteTypeID == nil || *clonedNote.NoteTypeID == srcType.ID {
		t.Fatalf("cloned note type id = %v, want a new note type of the cloned deck", clonedNote.NoteTypeID)
	}
	var clonedType NoteType
	if err := db.WithContext(ctx).First(&clonedType, "id = ?", *clonedNote.NoteTypeID).Error; err != nil {
		t.Fatalf("load cloned note type: %v", err)
	}
	if clonedType.DeckID != clone.ID {
		t.Errorf("cloned note type deck = %d, want %d", clonedType.DeckID, clone.ID)
	}
	if clonedType.PublicID == srcType.PublicID {
		t.Error("cloned note type kept the source public id, want a new row with its own id")
	}
	clonedCfg, err := clonedType.Config()
	if err != nil {
		t.Fatalf("decode cloned config: %v", err)
	}
	if clonedCfg.CSS != cfg.CSS {
		t.Errorf("cloned CSS = %q, want the source CSS %q kept", clonedCfg.CSS, cfg.CSS)
	}
	if len(clonedCfg.Fields) != len(cfg.Fields) || clonedCfg.Fields[0].ID != cfg.Fields[0].ID || clonedCfg.Fields[1].ID != cfg.Fields[1].ID {
		t.Errorf("cloned field ids = %+v, want the source ids %q and %q", clonedCfg.Fields, cfg.Fields[0].ID, cfg.Fields[1].ID)
	}
	if len(clonedCfg.Templates) != len(cfg.Templates) || clonedCfg.Templates[0].ID != cfg.Templates[0].ID {
		t.Errorf("cloned template ids = %+v, want the source id %q", clonedCfg.Templates, cfg.Templates[0].ID)
	}
	if (clonedCfg.AnkiOrigin == nil) != (cfg.AnkiOrigin == nil) {
		t.Errorf("cloned AnkiOrigin = %+v, want the source %+v", clonedCfg.AnkiOrigin, cfg.AnkiOrigin)
	}

	wantHTML := renderClonedFront(t, clonedCfg, NoteFieldValues(clonedNote.FieldsJSON))
	if err := NewDeckStore(db).Delete(ctx, owner, src.ID); err != nil {
		t.Fatalf("delete source deck: %v", err)
	}
	if err := db.WithContext(ctx).First(&clonedNote, "id = ?", clonedNote.ID).Error; err != nil {
		t.Fatalf("reload cloned note after source delete: %v", err)
	}
	if err := db.WithContext(ctx).First(&clonedType, "id = ?", clonedType.ID).Error; err != nil {
		t.Fatalf("reload cloned note type after source delete: %v", err)
	}
	clonedCfg, err = clonedType.Config()
	if err != nil {
		t.Fatalf("decode cloned config after source delete: %v", err)
	}
	if got := renderClonedFront(t, clonedCfg, NoteFieldValues(clonedNote.FieldsJSON)); got != wantHTML {
		t.Errorf("render after source delete = %q, want %q", got, wantHTML)
	}
}

func renderClonedFront(t *testing.T, cfg notetype.Config, fields map[string]string) string {
	t.Helper()
	if len(cfg.Templates) == 0 {
		t.Fatal("note type has no template to render")
	}
	out, err := notetype.Render(cfg, cfg.Templates[0], notetype.SideFront, notetype.RenderData{Fields: fields})
	if err != nil {
		t.Fatalf("render cloned front: %v", err)
	}
	return out
}
