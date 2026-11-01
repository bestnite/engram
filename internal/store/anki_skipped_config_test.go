package store

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"
)

func skippedConfigArchive(t *testing.T, topBad, otherBad, single bool) []byte {
	t.Helper()
	raw, err := os.ReadFile(ankiFixturePath("stock.anki21b.apkg"))
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := anki.ReadWithLimits(bytes.NewReader(raw), int64(len(raw)), DefaultPackageImportLimits().ankiLimits())
	if err != nil {
		t.Fatal(err)
	}
	plan := planAnkiDecks(pkg)
	var configID int64
	for _, deck := range pkg.Decks {
		if ankiDeckName(deck.Name) == plan.TopName {
			configID = deck.ConfigID
			break
		}
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[f.Name], err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := dec.DecodeAll(entries["collection.anki21b"], nil)
	dec.Close()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "collection.sqlite")
	if err := os.WriteFile(path, collection, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var config []byte
	addFloat := func(n protowire.Number, f float32) {
		config = protowire.AppendTag(config, n, protowire.Fixed32Type)
		config = protowire.AppendFixed32(config, math.Float32bits(f))
	}
	addFloat(1, 3)
	addFloat(1, 12)
	addFloat(2, 7)
	addFloat(37, .75)
	config = protowire.AppendTag(config, 16, protowire.VarintType)
	config = protowire.AppendVarint(config, 1234)
	for _, w := range packageTestWeights() {
		addFloat(6, float32(w))
	}
	bad := []byte{0x32, 1, 0}
	if _, err := db.Exec(`DELETE FROM deck_config`); err != nil {
		t.Fatal(err)
	}
	if !single {
		data := config
		if topBad {
			data = bad
		}
		if _, err := db.Exec(`INSERT INTO deck_config (id, name, mtime_secs, usn, config) VALUES (?, ?, 0, 0, ?)`, configID, "Top", data); err != nil {
			t.Fatal(err)
		}
	}
	data := config
	if !single {
		data = append([]byte(nil), config...)
		data = protowire.AppendTag(data, 16, protowire.VarintType)
		data = protowire.AppendVarint(data, 4321)
	}
	if otherBad {
		data = bad
	}
	if _, err := db.Exec(`INSERT INTO deck_config (id, name, mtime_secs, usn, config) VALUES (?, ?, 0, 0, ?)`, configID+10000, "Other", data); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	collection, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	entries["collection.anki21b"] = enc.EncodeAll(collection, nil)
	enc.Close()
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

func TestAnkiImportSkippedDeckConfigs(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "skipped_config")[0]
			for _, tc := range []struct {
				name                               string
				topBad, otherBad, single, defaults bool
			}{
				{"top invalid", true, false, false, true},
				{"other invalid", false, true, false, false},
				{"both valid", false, false, false, false},
				{"single different id", false, false, true, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					raw := skippedConfigArchive(t, tc.topBad, tc.otherBad, tc.single)
					report, err := NewDeckStore(db).ImportPackage(context.Background(), owner, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, ApplyWeights: true, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					if report.DeckConfigSkipped != tc.topBad {
						t.Fatalf("DeckConfigSkipped = %v, want %v", report.DeckConfigSkipped, tc.topBad)
					}
					got := deckPreset(t, db, report.DeckID)
					if tc.defaults {
						want := NewPreset(owner, got.Name)
						got.ID, got.PublicID, got.CreatedAt, got.UpdatedAt = want.ID, want.PublicID, want.CreatedAt, want.UpdatedAt
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("preset = %+v, defaults = %+v", got, want)
						}
						if report.WeightsApplied {
							t.Fatal("unexpected weights")
						}
						return
					}
					if got.LearningSteps != "3m,12m" || got.RelearningSteps != "7m" || got.MaximumIntervalDays != 1234 || got.DesiredRetention != .75 || got.WeightsJSON == nil || !report.WeightsApplied {
						t.Fatalf("preset = %+v, report = %+v", got, report)
					}
					var weights []float64
					if err := json.Unmarshal([]byte(*got.WeightsJSON), &weights); err != nil {
						t.Fatal(err)
					}
					want := packageTestWeights()
					for i := range want {
						want[i] = float64(float32(want[i]))
					}
					if !reflect.DeepEqual(weights, want) {
						t.Fatalf("weights = %v, want %v", weights, want)
					}
				})
			}
		})
	}
}
