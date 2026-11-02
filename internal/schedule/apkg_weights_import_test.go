package schedule

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/apkg"
	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"
)

// oldWeightsArchive writes the historical keys into the actual schema-11 collection.
func oldWeightsArchive(t *testing.T, input []byte, key string, weights []float64) []byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	w := zip.NewWriter(&output)
	for _, f := range r.File {
		reader, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if f.Name == "collection.anki21" {
			path := filepath.Join(t.TempDir(), "collection.db")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var raw string
			if err := db.QueryRow("SELECT dconf FROM col").Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var configs map[string]map[string]any
			if err := json.Unmarshal([]byte(raw), &configs); err != nil {
				t.Fatal(err)
			}
			for _, config := range configs {
				config["fsrsParams6"] = []float64{}
				config[key] = weights
			}
			encoded, err := json.Marshal(configs)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE col SET dconf = ?", string(encoded)); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
		}
		out, err := w.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestAnkiImportWeightsSchedule(t *testing.T) {
	defaults := fsrs.DefaultWeights()
	valid := append([]float64(nil), defaults[:]...)
	valid[0] += 0.1
	modern := []float64{0.5, 1.5, 2.5, 3.5, 4.5, 3.5, 3.5, 0.05, 1.0, 0.2, 0.8, 1.5, 0.06, 0.3, 1.6, 0.6, 1.9, 0.5, 0.1, 0.07, 0.15}
	// Modern Anki stores protobuf weights as float32.
	for i := range modern {
		modern[i] = float64(float32(modern[i]))
	}
	cases := []struct {
		name                                        string
		weights                                     []float64
		key                                         string
		modern, apply, existing, applied, discarded bool
	}{
		{"option off", valid, "", false, false, false, false, true},
		{"valid", valid, "", false, true, false, true, false},
		{"invalid", []float64{1, 2, 3}, "", false, true, false, false, true},
		{"no weights", nil, "", false, true, false, false, false},
		{"existing deck", valid, "", false, true, true, false, true},
		{"fsrsWeights 17", valid[:17], "fsrsWeights", false, true, false, true, false},
		{"fsrsParams5 19", valid[:19], "fsrsParams5", false, true, false, true, false},
		{"schema 18", modern, "", true, true, false, true, false},
	}
	dbs := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if db, ok := pgtest.Open(t); ok {
		if err := db.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatal(err)
		}
		dbs["postgres"] = db
	}
	for driver, db := range dbs {
		t.Run(driver, func(t *testing.T) {
			user := store.User{Username: "weights", Email: "weights@example.com", DisplayName: "weights", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					pkg := apkg.WritePackage{Collection: apkg.WriteCollection{
						CreatedAt:   1700000000,
						NoteTypes:   []apkg.WriteNoteType{{ID: 10, Name: "Basic", Fields: []apkg.Field{{Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []apkg.Template{{Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"}}}},
						Decks:       []apkg.WriteDeck{{ID: 20, Name: "Weights", ConfigID: 30}},
						DeckConfigs: []apkg.WriteDeckConfig{{ID: 30, Name: "Imported", DesiredRetention: .85, LearningSteps: []float64{2, 12}, RelearningSteps: []float64{3}, MaximumIntervalDays: 123, FSRSWeights: tc.weights}},
						Notes:       []apkg.WriteNote{{ID: 40, GUID: "test-guid", NoteTypeID: 10, Fields: []string{"question", "answer"}}}, Cards: []apkg.WriteCard{{ID: 50, NoteID: 40, DeckID: 20}},
					}}
					var raw bytes.Buffer
					if err := apkg.WriteArchive(&raw, &pkg); err != nil {
						t.Fatal(err)
					}
					data := raw.Bytes()
					if tc.key != "" {
						data = oldWeightsArchive(t, data, tc.key, tc.weights)
					}
					if tc.modern {
						var err error
						data, err = os.ReadFile("../notetype/testdata/anki/weights.anki21b.apkg")
						if err != nil {
							t.Fatal(err)
						}
					}
					opts := store.PackageImportOptions{Target: store.PackageTargetNewDeck, ApplyWeights: tc.apply, IncludeProgress: true, MediaRoot: t.TempDir()}
					if tc.existing {
						id := seedDeck(t, db, time.Now())
						opts.Target = fmt.Sprintf("into_deck:%d", id)
					}
					report, err := store.NewDeckStore(db).ImportPackage(t.Context(), user.ID, bytes.NewReader(data), opts)
					if err != nil {
						t.Fatal(err)
					}
					if report.WeightsApplied != tc.applied || report.WeightsDiscarded != tc.discarded {
						t.Fatalf("report: %+v", report)
					}
					var deck store.Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var preset store.Preset
					if err := db.First(&preset, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					if (preset.WeightsJSON != nil) != tc.applied {
						t.Fatalf("weights: %v", preset.WeightsJSON)
					}
					if !tc.existing && !tc.modern && (preset.DesiredRetention != .85 || preset.LearningSteps != "2m,12m" || preset.RelearningSteps != "3m" || preset.MaximumIntervalDays != 123) {
						t.Fatalf("preset: %+v", preset)
					}
					s, err := NewScheduler(&preset)
					if err != nil {
						t.Fatal(err)
					}
					want := defaults
					if tc.applied {
						want, err = fsrs.MigrateWeights(tc.weights)
						if err != nil {
							t.Fatal(err)
						}
					}
					params, err := Parameters(&preset)
					if err != nil {
						t.Fatal(err)
					}
					if params.W != want {
						t.Fatalf("migrated weights: got %v, want %v", params.W, want)
					}
					// go-fsrs clips migrated weights to its supported ranges during construction.
					params.W = want
					wantScheduler := fsrs.NewFSRS(params).Parameters.W
					if got := s.fsrs.Parameters.W; got != wantScheduler {
						t.Fatalf("scheduler weights: got %v, want %v", got, wantScheduler)
					}
					var states []store.CardState
					if err := db.Where("user_id = ? AND card_id IN (SELECT cards.id FROM cards JOIN notes ON notes.id = cards.note_id WHERE notes.deck_id = ?)", user.ID, deck.ID).Find(&states).Error; err != nil {
						t.Fatal(err)
					}
					if len(states) == 0 {
						t.Fatal("no imported card states")
					}
					for i := range states {
						if _, err := s.Preview(&states[i], time.Unix(1700000000, 0)); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}
