package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/notetype"
)

// clozeMetadataConfig is a valid cloze config that grades cloze blanks and has no metadata
// field yet. The write paths must add the field through notetype.EnsureMetadataField.
func clozeMetadataConfig() notetype.Config {
	return notetype.Config{
		Kind:        notetype.KindCloze,
		Fields:      []notetype.Field{{ID: "text", Name: "text", Font: "Arial", FontSize: 20}},
		Templates:   []notetype.Template{{ID: "cloze", Name: "Cloze", QFormat: "{{cloze:text}}{{type:cloze:text}}", AFormat: "{{cloze:text}}", Answer: &notetype.AnswerSpec{Control: "cloze_blanks", Bindings: map[string]string{"text": "text"}}}},
		CSS:         ".card {}",
		SortFieldID: "text",
	}
}

func metadataFieldOf(t *testing.T, cfg notetype.Config) notetype.Field {
	t.Helper()
	for _, field := range cfg.Fields {
		if field.Name == notetype.MetadataFieldName {
			return field
		}
	}
	return notetype.Field{}
}

// TestMetadataFieldOnCreateCopyAndImport proves the built-in field follows the note type through
// every write path: creating a cloze_blanks type, copying it into another deck, and importing a
// package that was exported without the field.
func TestMetadataFieldOnCreateCopyAndImport(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "metadata_builtin_"+driver)[0]
			deck := seedDeck(t, db, owner)
			copyDeck := seedDeck(t, db, owner)

			cfg := clozeMetadataConfig()
			nt := NoteType{DeckID: deck, Name: "Cloze custom"}
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatalf("create cloze note type: %v", err)
			}
			stored, err := nt.Config()
			if err != nil {
				t.Fatal(err)
			}
			field := metadataFieldOf(t, stored)
			if field.ID == "" {
				t.Fatalf("created cloze type has no metadata field: %+v", stored.Fields)
			}
			if !field.PlainText || !field.Collapsed || !field.ExcludeFromSearch || !field.PreventDeletion {
				t.Errorf("created metadata field flags = %+v", field)
			}

			// A config that does not grade cloze blanks keeps its exact field list.
			normal := noteTypeConfig("Card 1")
			plain := NoteType{DeckID: deck, Name: "Plain"}
			if err := NewNoteTypeStore(db).Create(ctx, &plain, normal); err != nil {
				t.Fatalf("create normal note type: %v", err)
			}
			plainCfg, err := plain.Config()
			if err != nil {
				t.Fatal(err)
			}
			if field := metadataFieldOf(t, plainCfg); field.ID != "" {
				t.Errorf("non-cloze type gained metadata field %+v", field)
			}

			// Copying the stored type into another deck applies the same rule.
			copied, err := cloneNoteType(ctx, db, copyDeck, &nt, stored)
			if err != nil {
				t.Fatalf("copy note type: %v", err)
			}
			copiedCfg, err := copied.Config()
			if err != nil {
				t.Fatal(err)
			}
			if field := metadataFieldOf(t, copiedCfg); field.ID == "" {
				t.Errorf("copied cloze type has no metadata field: %+v", copiedCfg.Fields)
			}

			// Importing a package exported without the field re-adds it through the import path.
			note := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
			if _, err := NewNoteStore(db).Create(ctx, &note, map[string]any{"text": "A {{c1::canonical}}.", "engram_meta": `{"cloze_accept":{"1":{"canonical":["alternate"]}}}`}); err != nil {
				t.Fatalf("create note: %v", err)
			}
			raw, err := NewDeckStore(db).ExportAnkiPackage(ctx, owner, deck, PackageOptions{IncludeEngramMeta: Ptr(false)})
			if err != nil {
				t.Fatalf("export without metadata: %v", err)
			}
			report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, NewDeckName: "Imported"})
			if err != nil {
				t.Fatalf("import without metadata: %v", err)
			}
			importedTypes, err := NewNoteTypeStore(db).ListByDeck(ctx, report.DeckID)
			if err != nil {
				t.Fatal(err)
			}
			if len(importedTypes) != 1 {
				t.Fatalf("imported note types = %d, want 1", len(importedTypes))
			}
			importedCfg, err := importedTypes[0].Config()
			if err != nil {
				t.Fatal(err)
			}
			if field := metadataFieldOf(t, importedCfg); field.ID == "" {
				t.Errorf("imported cloze type has no metadata field: %+v", importedCfg.Fields)
			}
		})
	}
}

// TestMetadataBindingRefused proves an answer spec cannot bind any role to the reserved field.
func TestMetadataBindingRefused(t *testing.T) {
	db := openMigrationDB(t)
	if err := db.AutoMigrate(AllModels()...); err != nil {
		t.Fatal(err)
	}
	cfg := clozeMetadataConfig()
	cfg.Fields = append(cfg.Fields, notetype.Field{ID: "meta-id", Name: notetype.MetadataFieldName})
	cfg.Templates[0].Answer.Bindings = map[string]string{"text": "text", "extra": "meta-id"}
	nt := NoteType{DeckID: 1, Name: "Bad binding"}
	err := NewNoteTypeStore(db).Create(context.Background(), &nt, cfg)
	var validation *notetype.ValidationError
	if !errors.As(err, &validation) || validation.Rule != notetype.RuleAnswerSpec {
		t.Fatalf("create with a metadata binding = %v, want RuleAnswerSpec", err)
	}
}

// TestEnsureClozeMetadataFieldMigration covers the stored-data migration on both databases:
// a missing field is appended, an existing field keeps its id and values, a corrupt config is
// skipped, and a second run writes nothing.
func TestEnsureClozeMetadataFieldMigration(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			seed := func(name string, cfg notetype.Config, version int) NoteType {
				raw, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				row := NoteType{DeckID: 1, Name: name, ConfigJSON: string(raw), Version: version, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
				if err := db.Create(&row).Error; err != nil {
					t.Fatalf("seed note type %q: %v", name, err)
				}
				return row
			}

			missing := seed("missing", clozeMetadataConfig(), 3)
			existing := clozeMetadataConfig()
			existing.Fields = append(existing.Fields, notetype.Field{ID: "meta-id", Name: notetype.MetadataFieldName, Description: "keep me", Font: "Courier"})
			existingRow := seed("existing", existing, 7)
			nonCloze := seed("normal", noteTypeConfig("Card 1"), 2)
			corrupt := NoteType{DeckID: 1, Name: "corrupt", ConfigJSON: `{not json`, Version: 5, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if err := db.Create(&corrupt).Error; err != nil {
				t.Fatalf("seed corrupt note type: %v", err)
			}

			if err := ensureClozeMetadataField(db); err != nil {
				t.Fatalf("ensureClozeMetadataField: %v", err)
			}
			read := func(id uint64) NoteType {
				var row NoteType
				if err := db.First(&row, id).Error; err != nil {
					t.Fatalf("read note type %d: %v", id, err)
				}
				return row
			}
			missingRow := read(missing.ID)
			if missingRow.Version != 4 {
				t.Errorf("migrated version = %d, want 4", missingRow.Version)
			}
			missingCfg, err := missingRow.Config()
			if err != nil {
				t.Fatal(err)
			}
			field := metadataFieldOf(t, missingCfg)
			if field.ID != notetype.MetadataFieldName {
				t.Fatalf("migrated metadata field = %+v, want id %q", field, notetype.MetadataFieldName)
			}
			if !field.PlainText || !field.Collapsed || !field.ExcludeFromSearch || !field.PreventDeletion {
				t.Errorf("migrated flags = %+v, want all four set", field)
			}

			existingRowAfter := read(existingRow.ID)
			if existingRowAfter.Version != 8 {
				t.Errorf("existing-field version = %d, want 8", existingRowAfter.Version)
			}
			existingCfg, err := existingRowAfter.Config()
			if err != nil {
				t.Fatal(err)
			}
			kept := metadataFieldOf(t, existingCfg)
			if kept.ID != "meta-id" || kept.Description != "keep me" || kept.Font != "Courier" {
				t.Errorf("existing metadata field = %+v, want id, description and font preserved", kept)
			}
			if !kept.PlainText || !kept.Collapsed || !kept.ExcludeFromSearch || !kept.PreventDeletion {
				t.Errorf("existing metadata flags = %+v, want all four set", kept)
			}

			if got := read(nonCloze.ID); got.Version != 2 {
				t.Errorf("non-cloze version changed to %d", got.Version)
			}
			if got := read(corrupt.ID); got.ConfigJSON != `{not json` || got.Version != 5 {
				t.Errorf("corrupt config row changed: %+v", got)
			}

			// A second run finds every eligible row already correct and writes nothing.
			before := map[uint64]int{}
			var rows []NoteType
			if err := db.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				before[row.ID] = row.Version
			}
			if err := ensureClozeMetadataField(db); err != nil {
				t.Fatalf("second ensureClozeMetadataField: %v", err)
			}
			if err := db.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if got := before[row.ID]; got != row.Version {
					t.Errorf("second run changed note type %d version %d -> %d", row.ID, got, row.Version)
				}
			}
		})
	}
}

// TestExportAnkiPackageEngramMetaDefaultAndOff pins both sides of the export switch: the default
// package carries the field and its values, and an export with the switch off carries neither
// while the remaining field ordinals and values stay aligned and the answer spec survives.
func TestExportAnkiPackageEngramMetaDefaultAndOff(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "metadata_export_"+driver)[0]
			deck := seedDeck(t, db, owner)
			cfg := clozeMetadataConfig()
			nt := NoteType{DeckID: deck, Name: "Cloze custom"}
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatal(err)
			}
			const text = "A {{c1::canonical}}."
			const meta = `{"cloze_accept":{"1":{"canonical":["alternate"]}}}`
			note := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
			if _, err := NewNoteStore(db).Create(ctx, &note, map[string]any{"text": text, "engram_meta": meta}); err != nil {
				t.Fatal(err)
			}

			type exportedField struct {
				Name string `json:"name"`
				Ord  int    `json:"ord"`
			}
			type exportedSpec struct {
				Control  string            `json:"control"`
				Bindings map[string]string `json:"bindings"`
			}
			type exportedModel struct {
				Name   string          `json:"name"`
				Sortf  int             `json:"sortf"`
				Fields []exportedField `json:"flds"`
				Engram *struct {
					AnswerSpecs map[string]exportedSpec `json:"answer_specs"`
				} `json:"engram"`
			}
			read := func(raw []byte) (exportedModel, []string) {
				t.Helper()
				col := readAnkiCollection(t, raw)
				var rawModels string
				if err := col.QueryRow("SELECT models FROM col").Scan(&rawModels); err != nil {
					t.Fatalf("read models: %v", err)
				}
				var models map[string]exportedModel
				if err := json.Unmarshal([]byte(rawModels), &models); err != nil {
					t.Fatalf("decode models: %v", err)
				}
				var model exportedModel
				for _, candidate := range models {
					if candidate.Name == "Cloze custom" {
						model = candidate
					}
				}
				if model.Name == "" {
					t.Fatalf("exported models do not contain Cloze custom: %s", rawModels)
				}
				var flds string
				if err := col.QueryRow("SELECT flds FROM notes").Scan(&flds); err != nil {
					t.Fatalf("read note fields: %v", err)
				}
				return model, strings.Split(flds, "\x1f")
			}

			withMeta, err := NewDeckStore(db).ExportAnkiPackage(ctx, owner, deck, PackageOptions{})
			if err != nil {
				t.Fatalf("default export: %v", err)
			}
			model, values := read(withMeta)
			metaIndex := -1
			for _, field := range model.Fields {
				if field.Name == notetype.MetadataFieldName {
					metaIndex = field.Ord
				}
			}
			if metaIndex < 0 {
				t.Fatalf("default export fields = %+v, want %s", model.Fields, notetype.MetadataFieldName)
			}
			if len(values) != len(model.Fields) || values[metaIndex] != meta {
				t.Errorf("default export note values = %q (fields %d), want metadata at ordinal %d", values, len(model.Fields), metaIndex)
			}
			defaultReport, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(withMeta), PackageImportOptions{Target: PackageTargetNewDeck, NewDeckName: "Round trip"})
			if err != nil {
				t.Fatalf("import default package: %v", err)
			}
			roundTripTypes, err := NewNoteTypeStore(db).ListByDeck(ctx, defaultReport.DeckID)
			if err != nil {
				t.Fatal(err)
			}
			if len(roundTripTypes) != 1 {
				t.Fatalf("round-trip note types = %d, want 1", len(roundTripTypes))
			}
			roundTripCfg, err := roundTripTypes[0].Config()
			if err != nil {
				t.Fatal(err)
			}
			roundTripField := metadataFieldOf(t, roundTripCfg)
			var roundTripNote Note
			if err := db.Where("deck_id = ?", defaultReport.DeckID).First(&roundTripNote).Error; err != nil {
				t.Fatal(err)
			}
			if got := NoteFieldValues(roundTripNote.FieldsJSON)[roundTripField.ID]; got != meta {
				t.Errorf("round-tripped metadata = %q, want %q", got, meta)
			}

			withoutMeta, err := NewDeckStore(db).ExportAnkiPackage(ctx, owner, deck, PackageOptions{IncludeEngramMeta: Ptr(false)})
			if err != nil {
				t.Fatalf("export without metadata: %v", err)
			}
			model, values = read(withoutMeta)
			if len(model.Fields) != 1 || model.Fields[0].Name != "text" {
				t.Fatalf("export without metadata fields = %+v, want only text", model.Fields)
			}
			if len(values) != 1 || values[0] != text {
				t.Errorf("export without metadata values = %q, want the text value only", values)
			}
			if model.Sortf != 0 {
				t.Errorf("sort field index = %d, want the first remaining field", model.Sortf)
			}
			if model.Engram == nil {
				t.Fatal("answer-spec key missing from the export without metadata")
			}
			spec, ok := model.Engram.AnswerSpecs["Cloze"]
			if !ok || spec.Control != "cloze_blanks" || spec.Bindings["text"] != "text" {
				t.Errorf("answer spec = %+v, want the cloze_blanks control bound by name", spec)
			}

			report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(withoutMeta), PackageImportOptions{Target: PackageTargetNewDeck, NewDeckName: "Imported"})
			if err != nil {
				t.Fatalf("import without metadata: %v", err)
			}
			importedTypes, err := NewNoteTypeStore(db).ListByDeck(ctx, report.DeckID)
			if err != nil {
				t.Fatal(err)
			}
			if len(importedTypes) != 1 {
				t.Fatalf("imported note types = %d, want 1", len(importedTypes))
			}
			importedCfg, err := importedTypes[0].Config()
			if err != nil {
				t.Fatal(err)
			}
			field := metadataFieldOf(t, importedCfg)
			if field.ID == "" {
				t.Fatalf("imported config has no metadata field: %+v", importedCfg.Fields)
			}
			var imported Note
			if err := db.Where("deck_id = ?", report.DeckID).First(&imported).Error; err != nil {
				t.Fatal(err)
			}
			if got := NoteFieldValues(imported.FieldsJSON)[field.ID]; got != "" {
				t.Errorf("imported metadata value = %q, want empty", got)
			}
		})
	}
}
