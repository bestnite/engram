package store

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func schedulingToleranceArchive(t *testing.T, modern bool, query string) []byte {
	t.Helper()
	fixture, collection := "stock.anki21.apkg", "collection.anki21"
	if modern {
		fixture, collection = "stock.anki21b.apkg", "collection.anki21b"
	}
	raw, err := os.ReadFile(ankiFixturePath(fixture))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := make(map[string][]byte)
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name], err = io.ReadAll(r)
		if closeErr := r.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	data := entries[collection]
	if modern {
		decoder, err := zstd.NewReader(nil)
		if err != nil {
			t.Fatal(err)
		}
		data, err = decoder.DecodeAll(data, nil)
		decoder.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "collection.sqlite")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var notes, cards int
	if err := db.QueryRow(`SELECT count(*) FROM notes`).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM cards`).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if notes == 0 || cards == 0 {
		t.Fatal("fixture has no content")
	}
	if _, err := db.Exec(`INSERT INTO revlog (id, cid, usn, ease, ivl, lastIvl, factor, time, type) SELECT 1, id, 0, 3, 1, 1, 2500, 100, 1 FROM cards LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(query)
	if err != nil {
		t.Fatal(err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		t.Fatalf("mutation affected %d rows: %v", affected, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if modern {
		encoder, err := zstd.NewWriter(nil)
		if err != nil {
			t.Fatal(err)
		}
		data = encoder.EncodeAll(data, nil)
		encoder.Close()
	}
	entries[collection] = data
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestAnkiImportSchedulingTolerance(t *testing.T) {
	tests := []struct {
		name, query string
		modern      bool
	}{
		{"real revlog interval", `UPDATE revlog SET ivl = 7.5`, false},
		{"real card due", `UPDATE cards SET due = 7.5`, false},
		{"real card factor", `UPDATE cards SET factor = 2500.5`, false},
		{"invalid dconf", `UPDATE col SET dconf = 'invalid'`, false},
		{"invalid deck config", `UPDATE deck_config SET config = x'320100'`, true},
	}
	golden := loadAnkiGolden(t, "stock")
	wantCards := 0
	for _, note := range golden.Notes {
		wantCards += len(note.Cards)
	}
	if len(golden.Notes) == 0 || wantCards == 0 {
		t.Fatal("golden has no content")
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					raw := schedulingToleranceArchive(t, tc.modern, tc.query)
					owner := seedUsers(t, db, "tolerance_"+tc.name)[0]
					report, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: false, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatalf("ImportPackage: %v", err)
					}
					if report.NotesCreated != len(golden.Notes) || report.CardsCreated != wantCards {
						t.Fatalf("report: notes = %d, cards = %d; want %d, %d", report.NotesCreated, report.CardsCreated, len(golden.Notes), wantCards)
					}
					var notes, cards int64
					if err := db.Model(&Note{}).Where("deck_id = ?", report.DeckID).Count(&notes).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Model(&Card{}).Where("note_id IN (?)", db.Model(&Note{}).Select("id").Where("deck_id = ?", report.DeckID)).Count(&cards).Error; err != nil {
						t.Fatal(err)
					}
					if notes != int64(len(golden.Notes)) || cards != int64(wantCards) {
						t.Fatalf("stored: notes = %d, cards = %d; want %d, %d", notes, cards, len(golden.Notes), wantCards)
					}
				})
			}
		})
	}
}
