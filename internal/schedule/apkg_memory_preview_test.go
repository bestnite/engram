package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/apkg"
	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
	"gorm.io/gorm"
)

func TestImportedAnkiMemoryPreview(t *testing.T) {
	dbs := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if db, ok := pgtest.Open(t); ok {
		if err := db.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatal(err)
		}
		dbs["postgres"] = db
	}
	for driver, db := range dbs {
		t.Run(driver, func(t *testing.T) {
			user := store.User{Username: "preview", Email: "preview@example.com", DisplayName: "preview", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name                   string
				kind, factor, interval int
				history, skipped       bool
			}{
				{"review_history", 2, 2500, 7, true, false},
				{"learning_history", 1, 0, -600, true, false},
				{"review_sm2", 2, 2500, 7, false, false},
				{"learning_invalid_sm2", 1, 0, -600, false, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					pkg := apkg.WritePackage{Collection: apkg.WriteCollection{
						CreatedAt:   1600000000,
						NoteTypes:   []apkg.WriteNoteType{{ID: 10, Name: "Basic", Fields: []apkg.Field{{Name: "Front"}}, Templates: []apkg.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
						Decks:       []apkg.WriteDeck{{ID: 20, Name: "Memory", ConfigID: 30}},
						DeckConfigs: []apkg.WriteDeckConfig{{ID: 30, Name: "Memory", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
						Notes:       []apkg.WriteNote{{ID: 40, GUID: "40", NoteTypeID: 10, Fields: []string{"question"}}},
						Cards:       []apkg.WriteCard{{ID: 50, NoteID: 40, DeckID: 20, Type: tc.kind, Queue: tc.kind, Ivl: tc.interval, Factor: tc.factor}},
					}}
					if tc.history {
						pkg.Collection.Reviews = []apkg.WriteReview{{ID: 1600000000000, CardID: 50, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: 50, Ease: 4, Type: 1}}
					}
					var archive bytes.Buffer
					if err := apkg.WriteArchive(&archive, &pkg); err != nil {
						t.Fatal(err)
					}
					report, err := store.NewDeckStore(db).ImportPackage(t.Context(), user.ID, &archive, store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					var states []store.CardState
					if err := db.Joins("JOIN cards ON cards.id = card_states.card_id").Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ? AND card_states.user_id = ?", report.DeckID, user.ID).Find(&states).Error; err != nil {
						t.Fatal(err)
					}
					want, skipped := 1, 0
					if tc.skipped {
						want, skipped = 0, 1
					}
					if len(states) != want || report.StatesSkipped != skipped {
						t.Fatalf("states=%+v report=%+v", states, report)
					}
					var deck store.Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var preset store.Preset
					if err := db.First(&preset, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					for i := range states {
						if _, err := mustScheduler(t, &preset).Preview(&states[i], time.Unix(1700000000, 0)); err != nil {
							t.Fatalf("state %+v: %v", states[i], err)
						}
					}
				})
			}
			for i, tc := range []struct{ s, d float64 }{{0, 5}, {5, 0}, {-1, 5}, {5, 11}, {5, 5}} {
				for _, history := range []bool{false, true} {
					t.Run(fmt.Sprintf("case_%d/history_%t", i, history), func(t *testing.T) {
						pkg := apkg.WritePackage{Collection: apkg.WriteCollection{
							CreatedAt:   1600000000,
							NoteTypes:   []apkg.WriteNoteType{{ID: 1000, Name: "Basic", Fields: []apkg.Field{{Ord: 0, Name: "Front"}, {Ord: 1, Name: "Back"}}, Templates: []apkg.Template{{Ord: 0, Name: "Card 1", QFormat: "{{Front}}", AFormat: "{{Back}}"}}}},
							Decks:       []apkg.WriteDeck{{ID: 1, Name: "Deck", ConfigID: 2}},
							DeckConfigs: []apkg.WriteDeckConfig{{ID: 2, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
							Notes:       []apkg.WriteNote{{ID: 1, GUID: "guid", NoteTypeID: 1000, Fields: []string{"front", "back"}}},
							Cards:       []apkg.WriteCard{{ID: 1, NoteID: 1, DeckID: 1, Type: 2, Queue: 2, Due: 5, Ivl: 7, Stability: &tc.s, Difficulty: &tc.d}},
						}}
						if history {
							pkg.Collection.Reviews = []apkg.WriteReview{{ID: 1600000000000, CardID: 1, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: 1, Ease: 4, Type: 1}}
						}
						var archive bytes.Buffer
						if err := apkg.WriteArchive(&archive, &pkg); err != nil {
							t.Fatal(err)
						}
						report, err := store.NewDeckStore(db).ImportPackage(context.Background(), user.ID, &archive, store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
						if err != nil {
							t.Fatal(err)
						}
						var states []store.CardState
						if err := db.Joins("JOIN cards ON cards.id = card_states.card_id").Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ? AND card_states.user_id = ?", report.DeckID, user.ID).Find(&states).Error; err != nil {
							t.Fatal(err)
						}
						want := 1
						if i < 4 && !history {
							want = 0
						}
						if len(states) != want {
							t.Fatalf("states=%+v report=%+v", states, report)
						}
						for j := range states {
							if _, err := mustScheduler(t, testPreset(t)).Preview(&states[j], time.Unix(1700000000, 0)); err != nil {
								t.Fatalf("Preview: %v", err)
							}
						}
					})
				}
			}
		})
	}
}

func TestAnkiResetRollbackPreview(t *testing.T) {
	dbs := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if db, ok := pgtest.Open(t); ok {
		if err := db.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatal(err)
		}
		dbs["postgres"] = db
	}
	for driver, db := range dbs {
		t.Run(driver, func(t *testing.T) {
			user := store.User{Username: "reset", Email: "reset@example.com", DisplayName: "reset", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
			if err := db.Create(&user).Error; err != nil {
				t.Fatal(err)
			}
			const start int64 = 1600000000000
			const day int64 = 86400000
			pkg := apkg.WritePackage{Collection: apkg.WriteCollection{
				CreatedAt:   1600000000,
				NoteTypes:   []apkg.WriteNoteType{{ID: 10, Name: "Basic", Fields: []apkg.Field{{Name: "Front"}}, Templates: []apkg.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
				Decks:       []apkg.WriteDeck{{ID: 20, Name: "Reset", ConfigID: 30}},
				DeckConfigs: []apkg.WriteDeckConfig{{ID: 30, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
				Notes:       []apkg.WriteNote{{ID: 40, GUID: "guid", NoteTypeID: 10, Fields: []string{"front"}}},
				Cards:       []apkg.WriteCard{{ID: 50, NoteID: 40, DeckID: 20, Type: 2, Queue: 2, Due: 210, Ivl: 7, Factor: 2500}},
				Reviews:     []apkg.WriteReview{{ID: start, CardID: 50, Ease: 3, Type: 0}, {ID: start + day, CardID: 50, Ease: 3, Type: 1, Ivl: 3}, {ID: start + 200*day, CardID: 50, Type: 4}, {ID: start + 201*day, CardID: 50, Ease: 3, Type: 0}, {ID: start + 204*day, CardID: 50, Ease: 3, Type: 1, Ivl: 7}},
			}}
			var archive bytes.Buffer
			if err := apkg.WriteArchive(&archive, &pkg); err != nil {
				t.Fatal(err)
			}
			report, err := store.NewDeckStore(db).ImportPackage(context.Background(), user.ID, &archive, store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var deck store.Deck
			if err := db.First(&deck, report.DeckID).Error; err != nil {
				t.Fatal(err)
			}
			var preset store.Preset
			if err := db.First(&preset, deck.PresetID).Error; err != nil {
				t.Fatal(err)
			}
			scheduler := mustScheduler(t, &preset)
			var state store.CardState
			if err := db.Where("user_id = ?", user.ID).First(&state).Error; err != nil {
				t.Fatal(err)
			}
			now := time.UnixMilli(start + 205*day)
			for i := 0; i < 2; i++ {
				state, err = undoOnce(t, db, state.CardID, user.ID, state.Version, scheduler, now)
				if err != nil {
					t.Fatalf("rollback %d: %v", i+1, err)
				}
			}
			if state.State != "new" {
				t.Fatalf("state = %+v, want new", state)
			}
			if _, err := scheduler.Preview(&state, now); err != nil {
				t.Fatalf("Preview: %v", err)
			}
			type snapshot struct {
				States  []store.CardState
				Reviews []store.Review
				Audits  []store.AuditLog
			}
			read := func() snapshot {
				var v snapshot
				if err := db.Order("card_id,user_id").Find(&v.States).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Order("id").Find(&v.Reviews).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Order("id").Find(&v.Audits).Error; err != nil {
					t.Fatal(err)
				}
				return v
			}
			before := read()
			tx := db.Begin()
			_, err = Rollback(context.Background(), tx, UndoInput{CardID: state.CardID, UserID: user.ID, ExpectedVersion: state.Version, Scheduler: scheduler, Now: now})
			if !errors.Is(err, ErrNothingToUndo) {
				t.Fatalf("pre-reset undo error=%v", err)
			}
			if err := tx.Commit().Error; err != nil {
				t.Fatal(err)
			}
			if after := read(); !reflect.DeepEqual(before, after) {
				t.Fatalf("pre-reset refusal changed tables: before=%+v after=%+v", before, after)
			}
		})
	}
}
