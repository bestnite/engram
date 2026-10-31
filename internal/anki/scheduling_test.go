package anki

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/encoding/protowire"
	"gorm.io/gorm"
)

func TestRealColumnRules(t *testing.T) {
	tests := []struct{ table, column, field, rule string }{
		{"col", "crt", "CreatedAt", "round"},
		{"cards", "id", "ID", "primary"}, {"cards", "nid", "", "fail"}, {"cards", "did", "", "fail"}, {"cards", "ord", "", "fail"},
		{"cards", "mod", "Modified", "round"}, {"cards", "usn", "USN", "round"}, {"cards", "due", "Due", "round"}, {"cards", "ivl", "Interval", "round"}, {"cards", "factor", "Factor", "round"}, {"cards", "reps", "Reps", "round"}, {"cards", "lapses", "Lapses", "round"}, {"cards", "left", "Left", "round"}, {"cards", "odue", "OriginalDue", "round"},
		{"cards", "type", "", "skip"}, {"cards", "queue", "", "skip"}, {"cards", "odid", "", "skip"}, {"cards", "flags", "", "skip"},
		{"revlog", "id", "ID", "primary"}, {"revlog", "cid", "", "revlog"}, {"revlog", "ease", "", "revlog"}, {"revlog", "type", "", "revlog"},
		{"revlog", "usn", "USN", "round"}, {"revlog", "ivl", "Interval", "round"}, {"revlog", "lastIvl", "LastInterval", "round"}, {"revlog", "factor", "Factor", "round"}, {"revlog", "time", "Time", "round"},
	}
	for _, tc := range tests {
		t.Run(tc.table+"/"+tc.column, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{
				`CREATE TABLE col (crt INTEGER, dconf TEXT)`, `INSERT INTO col VALUES (1, '{}')`,
				`CREATE TABLE cards (id INTEGER PRIMARY KEY, nid INTEGER, did INTEGER, ord INTEGER, mod INTEGER, usn INTEGER, type INTEGER, queue INTEGER, due INTEGER, ivl INTEGER, factor INTEGER, reps INTEGER, lapses INTEGER, left INTEGER, odue INTEGER, odid INTEGER, flags INTEGER, data TEXT)`,
				`INSERT INTO cards VALUES (1,1,1,0,1,1,1,1,1,1,1,1,1,1,1,1,1,''), (2,1,1,0,1,1,1,1,1,1,1,1,1,1,1,1,1,'')`,
				`CREATE TABLE revlog (id INTEGER PRIMARY KEY, cid INTEGER, usn INTEGER, ease INTEGER, ivl INTEGER, lastIvl INTEGER, factor INTEGER, time INTEGER, type INTEGER)`,
				`INSERT INTO revlog VALUES (1,1,1,1,1,1,1,1,1), (2,2,1,1,1,1,1,1,1)`,
			} {
				if err := db.Exec(q).Error; err != nil {
					t.Fatal(err)
				}
			}
			where := " WHERE id = 1"
			if tc.table == "col" {
				where = ""
			}
			err = db.Exec(fmt.Sprintf("UPDATE %s SET %s = 7.5%s", tc.table, tc.column, where)).Error
			if tc.rule == "primary" {
				if err == nil {
					t.Fatal("SQLite accepted REAL primary key")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			pkg := &Package{}
			pkg.Cards, err = readCards(db, pkg)
			if tc.rule == "fail" {
				if !errors.Is(err, ErrCorruptCollection) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = readScheduling(db, pkg); err != nil {
				t.Fatal(err)
			}
			switch tc.rule {
			case "skip":
				if pkg.CardsSkipped != 1 || len(pkg.Cards) != 1 || pkg.Cards[0].ID != 2 {
					t.Fatalf("cards = %+v, skipped = %d", pkg.Cards, pkg.CardsSkipped)
				}
			case "revlog":
				if !errors.Is(pkg.RevlogErr, ErrCorruptCollection) || len(pkg.Reviews) != 1 || pkg.Reviews[0].ID != 2 {
					t.Fatalf("reviews = %+v, error = %v", pkg.Reviews, pkg.RevlogErr)
				}
			case "round":
				var v reflect.Value
				switch tc.table {
				case "col":
					v = reflect.ValueOf(*pkg)
				case "cards":
					v = reflect.ValueOf(pkg.Cards[0])
				case "revlog":
					v = reflect.ValueOf(pkg.Reviews[0])
				}
				if got := v.FieldByName(tc.field).Int(); got != 8 {
					t.Fatalf("got %d, want 8", got)
				}
			}
		})
	}
}

func TestSchedulingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`CREATE TABLE col (crt INTEGER, dconf TEXT)`,
		`INSERT INTO col VALUES (100, '{"1":{"id":1,"name":"x","desiredRetention":0.91,"fsrsParams6":[1,2],"fsrsParams5":[3],"fsrsWeights":[4],"new":{"delays":[1,10]},"lapse":{"delays":[5]},"rev":{"maxIvl":200}}}')`,
		`CREATE TABLE revlog (id INTEGER, cid INTEGER, usn INTEGER, ease INTEGER, ivl INTEGER, lastIvl INTEGER, factor INTEGER, time INTEGER, type INTEGER)`,
		`INSERT INTO revlog VALUES (123, 456, -1, 3, 7, 2, 2500, 800, 1)`,
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	pkg := &Package{Format: FormatLegacy1}
	if err := readScheduling(db, pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Reviews) != 1 || pkg.Reviews[0] != (Review{123, 456, -1, 3, 7, 2, 2500, 800, 1}) {
		t.Fatalf("reviews: %+v", pkg.Reviews)
	}
	if len(pkg.DeckConfigs) != 1 || !reflect.DeepEqual(pkg.DeckConfigs[0].Weights, []float64{1, 2}) || pkg.DeckConfigs[0].MaximumReviewInterval != 200 {
		t.Fatalf("configs: %+v", pkg.DeckConfigs)
	}
}

func TestDeckConfigsSkipped(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modern bool
		setup  []string
		want   bool
	}{
		{"legacy invalid", false, []string{`CREATE TABLE col (dconf TEXT)`, `INSERT INTO col VALUES ('invalid')`}, true},
		{"legacy valid", false, []string{`CREATE TABLE col (dconf TEXT)`, `INSERT INTO col VALUES ('{"1":{"id":1}}')`}, false},
		{"modern invalid row", true, []string{`CREATE TABLE deck_config (id INTEGER, name TEXT, config BLOB)`, `INSERT INTO deck_config VALUES (1, 'bad', x'320100'), (2, 'good', x'')`}, true},
		{"modern valid rows", true, []string{`CREATE TABLE deck_config (id INTEGER, name TEXT, config BLOB)`, `INSERT INTO deck_config VALUES (1, 'one', x''), (2, 'two', x'')`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range tc.setup {
				if err := db.Exec(q).Error; err != nil {
					t.Fatal(err)
				}
			}
			pkg := &Package{}
			if tc.modern {
				err = readModernDeckConfigs(db, pkg)
			} else {
				err = readLegacyDeckConfigs(db, pkg)
			}
			if err != nil {
				t.Fatal(err)
			}
			if pkg.DeckConfigsSkipped != tc.want {
				t.Fatalf("DeckConfigsSkipped = %v, want %v", pkg.DeckConfigsSkipped, tc.want)
			}
		})
	}
}

func TestSchedulingTolerance(t *testing.T) {
	tests := []struct {
		name, query string
		modern      bool
		check       func(*testing.T, *Package)
	}{
		{"revlog real", `UPDATE revlog SET ivl = 7.5`, false, func(t *testing.T, p *Package) {
			if len(p.Reviews) != 1 || p.Reviews[0].Interval != 8 {
				t.Fatalf("reviews: %+v", p.Reviews)
			}
		}},
		{"card due real", `UPDATE cards SET due = 7.5`, false, func(t *testing.T, p *Package) {
			if p.Cards[0].Due != 8 {
				t.Fatalf("due: %d", p.Cards[0].Due)
			}
		}},
		{"card factor real", `UPDATE cards SET factor = 2500.5`, false, func(t *testing.T, p *Package) {
			if p.Cards[0].Factor != 2501 {
				t.Fatalf("factor: %d", p.Cards[0].Factor)
			}
		}},
		{"invalid dconf", `UPDATE col SET dconf = 'invalid'`, false, func(t *testing.T, p *Package) {
			if len(p.DeckConfigs) != 0 || !p.DeckConfigsSkipped {
				t.Fatalf("configs: %+v, skipped: %v", p.DeckConfigs, p.DeckConfigsSkipped)
			}
		}},
		{"one malformed dconf entry", `UPDATE col SET dconf = '{"1":{"id":1,"rev":{"maxIvl":36500.0}},"2":{"id":2,"name":"valid","rev":{"maxIvl":200}}}'`, false, func(t *testing.T, p *Package) {
			want := []DeckConfig{{ID: 2, Name: "valid", MaximumReviewInterval: 200}}
			if !reflect.DeepEqual(p.DeckConfigs, want) || !p.DeckConfigsSkipped {
				t.Fatalf("configs: %+v, skipped: %v", p.DeckConfigs, p.DeckConfigsSkipped)
			}
		}},
		{"invalid config protobuf", `UPDATE deck_config SET config = x'320100'`, true, func(t *testing.T, p *Package) {
			if len(p.DeckConfigs) != 0 {
				t.Fatalf("configs: %+v", p.DeckConfigs)
			}
		}},
		{"unreadable revlog", `UPDATE revlog SET ivl = 'invalid'`, false, func(t *testing.T, p *Package) {
			if p.RevlogErr == nil {
				t.Fatal("missing revlog error")
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var archive []byte
			if tc.modern {
				var err error
				archive, err = os.ReadFile(filepath.Join("..", "notetype", "testdata", "anki", "stock.anki21b.apkg"))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var b bytes.Buffer
				if err := WriteArchive(&b, writeTestPackage()); err != nil {
					t.Fatal(err)
				}
				archive = b.Bytes()
			}
			zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
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
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			name := "collection.anki21"
			if tc.modern {
				name = "collection.anki21b"
				decoder, err := zstd.NewReader(nil)
				if err != nil {
					t.Fatal(err)
				}
				entries[name], err = decoder.DecodeAll(entries[name], nil)
				decoder.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(t.TempDir(), "collection.sqlite")
			if err := os.WriteFile(path, entries[name], 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			if tc.name == "unreadable revlog" {
				if _, err := db.Exec(`INSERT INTO revlog VALUES (1600000000001, 1, 0, 3, 9, 1, 2500, 100, 1)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			entries[name], err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.modern {
				encoder, err := zstd.NewWriter(nil)
				if err != nil {
					t.Fatal(err)
				}
				entries[name] = encoder.EncodeAll(entries[name], nil)
				encoder.Close()
			}
			var b bytes.Buffer
			zw := zip.NewWriter(&b)
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
			pkg, err := ReadWithLimits(bytes.NewReader(b.Bytes()), int64(b.Len()), DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if len(pkg.Notes) == 0 || len(pkg.Cards) == 0 {
				t.Fatalf("missing content: %+v", pkg)
			}
			if tc.name != "unreadable revlog" && pkg.RevlogErr != nil {
				t.Fatalf("revlog: %v", pkg.RevlogErr)
			}
			if tc.name == "unreadable revlog" && (len(pkg.Reviews) != 1 || pkg.Reviews[0].Interval != 9) {
				t.Fatalf("readable reviews: %+v", pkg.Reviews)
			}
			tc.check(t, pkg)
		})
	}
}

func TestDecodeDeckConfig(t *testing.T) {
	var b []byte
	packed := func(num protowire.Number, values ...float32) {
		var raw []byte
		for _, v := range values {
			var bits [4]byte
			binary.LittleEndian.PutUint32(bits[:], math.Float32bits(v))
			raw = append(raw, bits[:]...)
		}
		b = protowire.AppendTag(b, num, protowire.BytesType)
		b = protowire.AppendBytes(b, raw)
	}
	packed(1, 1, 10)
	packed(2, 5)
	packed(3, 1)
	packed(5, 2)
	packed(6, 3, 4)
	b = protowire.AppendTag(b, 16, protowire.VarintType)
	b = protowire.AppendVarint(b, 1000)
	b = protowire.AppendTag(b, 37, protowire.Fixed32Type)
	b = protowire.AppendFixed32(b, math.Float32bits(.9))
	cfg, err := decodeDeckConfig(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Weights, []float64{3, 4}) || !reflect.DeepEqual(cfg.LearnSteps, []float64{1, 10}) || !reflect.DeepEqual(cfg.RelearnSteps, []float64{5}) || cfg.MaximumReviewInterval != 1000 || cfg.DesiredRetention < .899 || cfg.DesiredRetention > .901 {
		t.Fatalf("config: %+v", cfg)
	}
	if _, err := decodeDeckConfig([]byte{0x32, 1, 0}); err == nil {
		t.Fatal("accepted malformed packed floats")
	}
}
