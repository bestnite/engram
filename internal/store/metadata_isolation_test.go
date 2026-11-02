package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"git.nite07.com/nite/engram/internal/apkg"
	"git.nite07.com/nite/engram/internal/notetype"
)

func TestMetadataIsolationNoteSave(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "metadata_owner", "metadata_actor")
			deck := seedDeck(t, db, users[1])
			sha := saveMedia(t, db, t.TempDir(), users[0], mediaBytes("metadata-secret"))
			cfg := notetype.Config{Kind: notetype.KindCloze, Fields: []notetype.Field{{ID: "body", Name: "Text"}, {ID: "meta-id", Name: notetype.MetadataFieldName}, {ID: "other", Name: "engram_notes"}}, Templates: []notetype.Template{{ID: "cloze", Name: "Cloze", QFormat: "{{cloze:Text}}"}}}
			nt := NoteType{Name: "Metadata", DeckID: deck}
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatal(err)
			}
			storedCfg, err := nt.Config()
			if err != nil {
				t.Fatal(err)
			}
			if !storedCfg.Fields[1].ExcludeFromSearch {
				t.Fatal("metadata search flag not set")
			}
			for _, metadata := range []string{`{"future":"{{c9::x}} [sound:` + sha + `] /media/` + sha + `"}`, `broken {{c9::x}} [sound:` + sha + `] /media/` + sha} {
				n := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
				cards, err := NewNoteStore(db).Save(WithActor(ctx, users[1]), &n, map[string]any{"Text": "{{c1::x}}", "engram_meta": metadata, "engram_notes": "{{c7::y}} ordinaryneedle"})
				if err != nil {
					t.Fatal(err)
				}
				if len(cards) != 2 {
					t.Fatalf("cards: %#v", cards)
				}
				if NoteFieldValues(n.FieldsJSON)["meta-id"] != metadata {
					t.Fatalf("metadata changed: %s", n.FieldsJSON)
				}
				if strings.Contains(n.SearchText, "future") || !strings.Contains(n.SearchText, "ordinaryneedle") {
					t.Fatalf("search text: %q", n.SearchText)
				}
				var count int64
				if err := db.Model(&MediaNote{}).Where("note_id = ?", n.ID).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("media mapping %d %v", count, err)
				}
				if err := db.Model(&MediaName{}).Where("deck_id = ?", deck).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("media names %d %v", count, err)
				}
				readable, err := mediaReadableByUser(ctx, db, users[1], sha)
				if err != nil || readable {
					t.Fatalf("readable %v %v", readable, err)
				}
			}
			impact, err := NewNoteTypeStore(db).Update(ctx, &nt, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0})
			if err != nil || impact.CardsAdded != 0 || impact.CardsRemoved != 0 {
				t.Fatalf("metadata-only update: %+v %v", impact, err)
			}
			ordinary := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
			_, err = NewNoteStore(db).Save(WithActor(ctx, users[1]), &ordinary, map[string]any{"Text": "{{c1::x}}", "engram_notes": "[sound:" + sha + "]"})
			var denied *MediaWriteError
			if !errors.As(err, &denied) {
				t.Fatalf("ordinary field must enforce media permission: %v", err)
			}
			if err := RecordMediaUploader(ctx, db, sha, users[1]); err != nil {
				t.Fatal(err)
			}
			ordinary = Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
			if _, err := NewNoteStore(db).Save(WithActor(ctx, users[1]), &ordinary, map[string]any{"Text": "{{c1::x}}", "engram_notes": "[sound:" + sha + "]"}); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&MediaNote{}).Where("note_id = ?", ordinary.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("ordinary mapping %d %v", count, err)
			}
		})
	}
}

func TestMetadataImportScansAndRewrite(t *testing.T) {
	pkg := &apkg.Package{NoteTypes: []apkg.NoteType{{ID: 1, Fields: []apkg.Field{{Name: "engram_meta"}, {Name: "engram_notes"}}}}, Notes: []apkg.Note{{NoteTypeID: 1, Fields: []string{`{"x":"[sound:hidden.mp3]"}`, "[sound:ordinary.mp3]"}}}}
	if got := ankiMediaRefs(pkg); !reflect.DeepEqual(got, []string{"ordinary.mp3"}) {
		t.Fatal(got)
	}
	forEachContentField(pkg, func(value *string) {
		*value = rewriteMediaRefs(*value, map[string]string{"hidden.mp3": "changed.mp3", "ordinary.mp3": "new.mp3"})
	})
	if !strings.Contains(pkg.Notes[0].Fields[0], "hidden.mp3") || pkg.Notes[0].Fields[1] != "[sound:new.mp3]" {
		t.Fatal(pkg.Notes[0].Fields)
	}
	if _, err := json.Marshal(pkg); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataSearchIgnoresMissingFlag(t *testing.T) {
	cfg := notetype.Config{Fields: []notetype.Field{
		{ID: "m", Name: notetype.MetadataFieldName},
		{ID: "o", Name: "engram_notes"},
	}}
	got := noteSearchText(cfg, map[string]string{"m": `{"hidden":"secretneedle"}`, "o": "ordinaryneedle"})
	if got != "ordinaryneedle" {
		t.Fatalf("search text = %q", got)
	}
}

func TestMetadataExportDoesNotCheckMedia(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "export_metadata_owner", "export_metadata_reader")
			deck := seedDeck(t, db, users[1])
			sha := saveMedia(t, db, t.TempDir(), users[0], mediaBytes("export-metadata"))
			cfg := noteTypeConfig("Card")
			cfg.Fields = append(cfg.Fields, notetype.Field{ID: "meta", Name: notetype.MetadataFieldName}, notetype.Field{ID: "ordinary", Name: "engram_notes"})
			nt := NoteType{DeckID: deck, Name: "Export"}
			if err := nt.SetConfig(nt.Name, cfg); err != nil {
				t.Fatal(err)
			}
			nt.ID = 123
			metadata := `{"x":"[sound:` + sha + `]"}`
			raw, err := json.Marshal(map[string]string{"meta": metadata})
			if err != nil {
				t.Fatal(err)
			}
			note := Note{ID: 456, NoteTypeID: Ptr(nt.ID), FieldsJSON: string(raw)}
			_, err = NewDeckStore(db).ankiMediaFiles(ctx, users[1], deck, []NoteType{nt}, []Note{note}, t.TempDir())
			if err != nil {
				t.Fatalf("metadata triggered export media scan: %v", err)
			}
			raw, err = json.Marshal(map[string]string{"ordinary": "[sound:" + sha + "]"})
			if err != nil {
				t.Fatal(err)
			}
			note.FieldsJSON = string(raw)
			_, err = NewDeckStore(db).ankiMediaFiles(ctx, users[1], deck, []NoteType{nt}, []Note{note}, t.TempDir())
			var denied *PackageError
			if !errors.As(err, &denied) || denied.Code != CodePackageMediaForbidden {
				t.Fatalf("ordinary field should enforce export permissions: %v", err)
			}
		})
	}
}

func TestMetadataRenameMediaAuthorization(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			users := seedUsers(t, db, "rename_owner", "rename_editor")
			deck := seedDeck(t, db, users[1])
			sha := saveMedia(t, db, t.TempDir(), users[0], mediaBytes("rename-secret"))
			cfg := noteTypeConfig("Card")
			cfg.Fields = append(cfg.Fields, notetype.Field{ID: "meta", Name: notetype.MetadataFieldName})
			nt := NoteType{DeckID: deck, Name: "Rename"}
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatal(err)
			}
			metadata := "[sound:" + sha + "]"
			note := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
			if _, err := NewNoteStore(db).Create(WithActor(ctx, users[1]), &note, map[string]any{"Front": "front", "engram_meta": metadata}); err != nil {
				t.Fatal(err)
			}
			cfg.Fields[2].Name = "Ordinary"
			var denied *MediaWriteError
			if _, err := NewNoteTypeStore(db).Update(WithActor(ctx, users[1]), &nt, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0}); !errors.As(err, &denied) {
				t.Fatalf("rename should be denied: %v", err)
			}
			var count int64
			if err := db.Model(&MediaNote{}).Where("note_id = ?", note.ID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("unexpected mapping %d: %v", count, err)
			}
			readable, err := mediaReadableByUser(ctx, db, users[1], sha)
			if err != nil || readable {
				t.Fatalf("unexpected access %v: %v", readable, err)
			}
			if err := RecordMediaUploader(ctx, db, sha, users[1]); err != nil {
				t.Fatal(err)
			}
			if _, err := NewNoteTypeStore(db).Update(WithActor(ctx, users[1]), &nt, cfg, NoteTypeUpdateOptions{ExpectedVersion: 0}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&MediaNote{}).Where("note_id = ?", note.ID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("mapping %d: %v", count, err)
			}
			cfg.Fields[2].Name = notetype.MetadataFieldName
			if _, err := NewNoteTypeStore(db).Update(WithActor(ctx, users[1]), &nt, cfg, NoteTypeUpdateOptions{ExpectedVersion: 1}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&MediaNote{}).Where("note_id = ?", note.ID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("mapping after rename %d: %v", count, err)
			}
		})
	}
}

func TestMetadataNoteSavePreservesRawJSON(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "metadata_raw")[0]
			deck := seedDeck(t, db, owner)
			cfg := noteTypeConfig("Card")
			cfg.Fields = append(cfg.Fields, notetype.Field{ID: "meta", Name: notetype.MetadataFieldName})
			nt := NoteType{DeckID: deck, Name: "Raw"}
			if err := NewNoteTypeStore(db).Create(ctx, &nt, cfg); err != nil {
				t.Fatal(err)
			}
			for _, raw := range []string{`{"cloze_accept":{},"future_key":{"nested":[1,{"x":true}]}}`, `<div>{&quot;bad&quot;:1}</div>`, `{"broken":`} {
				note := Note{DeckID: deck, NoteTypeID: Ptr(nt.ID)}
				if _, err := NewNoteStore(db).Create(ctx, &note, map[string]any{"Front": "before", "engram_meta": raw}); err != nil {
					t.Fatal(err)
				}
				if _, err := NewNoteStore(db).Update(ctx, &note, map[string]any{"Front": "after", "engram_meta": raw}); err != nil {
					t.Fatal(err)
				}
				got, err := NewNoteStore(db).ByID(ctx, note.ID)
				if err != nil {
					t.Fatal(err)
				}
				if NoteFieldValues(got.FieldsJSON)["meta"] != raw {
					t.Fatalf("raw metadata changed: %s", got.FieldsJSON)
				}
			}
		})
	}
}
