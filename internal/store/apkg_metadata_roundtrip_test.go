package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"git.nite07.com/nite/engram/internal/answer"
	"git.nite07.com/nite/engram/internal/notetype"
)

func TestAnkiMetadataActualRoundTrip(t *testing.T) {
	python := os.Getenv("ENGRAM_ANKI_PYTHON")
	if python == "" {
		t.Skip("set ENGRAM_ANKI_PYTHON to the pinned Anki Python")
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			owner := seedUsers(t, db, "metadata_roundtrip")[0]
			deck := seedDeck(t, db, owner)
			cfg := notetype.Config{Kind: notetype.KindCloze, Fields: []notetype.Field{{ID: "text", Name: "Text"}, {ID: "meta", Name: "engram_meta"}, {ID: "ordinary", Name: "engram_notes"}}, Templates: []notetype.Template{{ID: "t", Name: "Cloze", QFormat: "{{cloze:Text}}{{type:Text}}", AFormat: "{{cloze:Text}}", Answer: &notetype.AnswerSpec{Control: "cloze_blanks", Bindings: map[string]string{"text": "text"}}}}}
			nt := NoteType{DeckID: deck, Name: "Metadata roundtrip"}
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatal(err)
			}
			const text = `A {{c1::canonical::hint}}.`
			const meta = `{"cloze_accept":{"1":{"canonical":["alternate"]}},"future_key":{"nested":[1,{"x":true}]}}`
			note := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
			if _, err := NewNoteStore(db).Create(ctx, &note, map[string]any{"Text": text, "engram_meta": meta, "engram_notes": "ordinary"}); err != nil {
				t.Fatal(err)
			}
			raw, err := NewDeckStore(db).ExportDeckPackage(ctx, owner, deck, PackageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			source, legacy, modern := filepath.Join(dir, "source.apkg"), filepath.Join(dir, "legacy.apkg"), filepath.Join(dir, "modern.apkg")
			if err := os.WriteFile(source, raw, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(python, "../../tools/anki-oracle/roundtrip_metadata.py", source, legacy, modern)
			output, err := cmd.CombinedOutput()
			t.Logf("oracle: %s", output)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{legacy, modern} {
				t.Run(filepath.Base(path), func(t *testing.T) {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					report, err := NewDeckStore(db).ImportPackage(ctx, owner, bytes.NewReader(data), PackageImportOptions{Target: PackageTargetNewDeck, NewDeckName: filepath.Base(path)})
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(report.ReservedPrefixFields, []string{"engram_notes"}) {
						t.Fatalf("prefix report: %+v", report)
					}
					var imported Note
					if err := db.Where("deck_id = ?", report.DeckID).First(&imported).Error; err != nil {
						t.Fatal(err)
					}
					var importedType NoteType
					if err := db.First(&importedType, *imported.NoteTypeID).Error; err != nil {
						t.Fatal(err)
					}
					c, err := importedType.Config()
					if err != nil {
						t.Fatal(err)
					}
					values := NoteFieldValues(imported.FieldsJSON)
					var actualText string
					for _, field := range c.Fields {
						if field.Name == "Text" {
							actualText = values[field.ID]
						}
					}
					if actualText != text {
						t.Fatalf("canonical %q", actualText)
					}
					var wantJSON, gotJSON any
					if err := json.Unmarshal([]byte(meta), &wantJSON); err != nil {
						t.Fatal(err)
					}
					metadata := answer.MetadataFieldValue(c, values)
					if err := json.Unmarshal([]byte(metadata), &gotJSON); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(wantJSON, gotJSON) {
						t.Fatalf("metadata %s", metadata)
					}
					spec := c.Templates[0].Answer
					if spec == nil {
						t.Fatal("missing answer spec")
					}
					bindings, err := answer.ResolveBindings(*spec, values)
					if err != nil {
						t.Fatal(err)
					}
					control, ok := answer.Lookup(spec.Control)
					if !ok {
						t.Fatal("unknown control")
					}
					grade, err := control.Grade(answer.GradeContext{Bindings: bindings, Metadata: metadata, ClozeOrdinal: 1, Answer: json.RawMessage(`["alternate"]`)})
					if err != nil || grade.Score != 1 {
						t.Fatalf("grade %+v %v", grade, err)
					}
				})
			}
		})
	}
}
