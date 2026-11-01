package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/pgtest"
	"git.nite07.com/nite/engram/internal/store"
	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"
)

func undoMemoryDatabases(t *testing.T) map[string]*gorm.DB {
	t.Helper()
	dbs := map[string]*gorm.DB{"sqlite": newTestDB(t)}
	if db, ok := pgtest.Open(t); ok {
		if err := db.AutoMigrate(store.AllModels()...); err != nil {
			t.Fatal(err)
		}
		dbs["postgres"] = db
	}
	return dbs
}

func TestAnkiImportedUndoMemoryReplay(t *testing.T) {
	for driver, db := range undoMemoryDatabases(t) {
		for _, tc := range []struct {
			name            string
			carried, review bool
		}{
			{"carried-learning", true, false}, {"review-sm2", false, true}, {"learning", false, false},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := store.User{Username: "undo-" + tc.name, Email: tc.name + "@example.com", DisplayName: "undo", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
				if err := db.Create(&user).Error; err != nil {
					t.Fatal(err)
				}
				card := anki.WriteCard{ID: 50, NoteID: 40, DeckID: 20, Type: 2, Queue: 2, Ivl: 30, Factor: 2500}
				if tc.carried {
					card.Stability, card.Difficulty = store.Ptr(5.0), store.Ptr(5.0)
				}
				first := anki.WriteReview{ID: 1600000000000, CardID: 50, Ease: 3, Type: 0, Ivl: 1, Factor: 2500}
				if tc.review {
					first.Type, first.Ivl = 1, 30
				}
				pkg := anki.WritePackage{Collection: anki.WriteCollection{CreatedAt: 1600000000,
					NoteTypes: []anki.WriteNoteType{{ID: 10, Name: "Basic", Fields: []anki.Field{{Name: "Front"}}, Templates: []anki.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
					Decks:     []anki.WriteDeck{{ID: 20, Name: "Memory", ConfigID: 30}}, DeckConfigs: []anki.WriteDeckConfig{{ID: 30, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
					Notes: []anki.WriteNote{{ID: 40, GUID: "guid", NoteTypeID: 10, Fields: []string{"front"}}}, Cards: []anki.WriteCard{card},
					Reviews: []anki.WriteReview{first, {ID: 1600259200000, CardID: 50, Ease: 3, Type: 1, Ivl: 30, Factor: 2500}},
				}}
				var raw bytes.Buffer
				if err := anki.WriteArchive(&raw, &pkg); err != nil {
					t.Fatal(err)
				}
				report, err := store.NewDeckStore(db).ImportPackage(context.Background(), user.ID, &raw, store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				var state store.CardState
				if err := db.Where("user_id = ?", user.ID).First(&state).Error; err != nil {
					t.Fatal(err)
				}
				var reviews []store.Review
				if err := db.Where("card_id = ? AND user_id = ?", state.CardID, user.ID).Order("id").Find(&reviews).Error; err != nil {
					t.Fatal(err)
				}
				if len(reviews) != 2 {
					t.Fatalf("reviews=%+v", reviews)
				}
				engine := fsrs.NewFSRS(fsrs.DefaultParam())
				var want *fsrs.MemoryState
				if tc.review {
					want, err = engine.MemoryStateFromSM2(2.5, 30, .9)
				} else {
					want, err = engine.MemoryState(fsrs.ReviewEntries{{Rating: fsrs.Good}}, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if reviews[0].Stability == nil || reviews[0].Difficulty == nil || math.Abs(*reviews[0].Stability-want.Stability) > 1e-4 || math.Abs(*reviews[0].Difficulty-want.Difficulty) > 1e-4 {
					t.Fatalf("first=%+v want=%+v", reviews[0], want)
				}
				if tc.carried && (reviews[1].Stability == nil || reviews[1].Difficulty == nil || *reviews[1].Stability != 5 || *reviews[1].Difficulty != 5) {
					t.Fatalf("last=%+v", reviews[1])
				}
				var deck store.Deck
				if err := db.First(&deck, report.DeckID).Error; err != nil {
					t.Fatal(err)
				}
				var preset store.Preset
				if err := db.First(&preset, deck.PresetID).Error; err != nil {
					t.Fatal(err)
				}
				s := mustScheduler(t, &preset)
				now := time.Unix(1700000000, 0)
				restored, err := undoOnce(t, db, state.CardID, user.ID, state.Version, s, now)
				if err != nil {
					t.Fatal(err)
				}
				if restored.Stability == nil || restored.Difficulty == nil || math.Abs(*restored.Stability-want.Stability) > 1e-4 || math.Abs(*restored.Difficulty-want.Difficulty) > 1e-4 {
					t.Fatalf("restored=%+v want=%+v", restored, want)
				}
				if _, err := s.Preview(&restored, now); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestAnkiImportedCramMemory(t *testing.T) {
	for driver, db := range undoMemoryDatabases(t) {
		for _, carried := range []bool{false, true} {
			for _, tail := range []bool{false, true} {
				name := fmt.Sprintf("%s/carried-%t/tail-%t", driver, carried, tail)
				t.Run(name, func(t *testing.T) {
					user := store.User{Username: name, Email: fmt.Sprintf("cram-%t-%t@example.com", carried, tail), DisplayName: "cram", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}
					if err := db.Create(&user).Error; err != nil {
						t.Fatal(err)
					}
					card := anki.WriteCard{ID: 50, NoteID: 40, DeckID: 20, Type: 2, Queue: 2, Ivl: 30, Factor: 2500}
					if carried {
						card.Stability, card.Difficulty = store.Ptr(5.0), store.Ptr(5.0)
					}
					rows := []anki.WriteReview{
						{ID: 1600000000000, CardID: 50, Ease: 3, Type: 0, Ivl: 1, Factor: 2500},
						{ID: 1600086400000, CardID: 50, Ease: 3, Type: 3, Ivl: 1, Factor: 0},
						{ID: 1600259200000, CardID: 50, Ease: 3, Type: 1, Ivl: 30, Factor: 2500},
					}
					if tail {
						rows = append(rows, anki.WriteReview{ID: 1600345600000, CardID: 50, Ease: 3, Type: 3, Ivl: 30}, anki.WriteReview{ID: 1600432000000, CardID: 50, Ease: 3, Type: 3, Ivl: 30})
					}
					pkg := anki.WritePackage{Collection: anki.WriteCollection{CreatedAt: 1600000000,
						NoteTypes: []anki.WriteNoteType{{ID: 10, Name: "Basic", Fields: []anki.Field{{Name: "Front"}}, Templates: []anki.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
						Decks:     []anki.WriteDeck{{ID: 20, Name: "Memory", ConfigID: 30}}, DeckConfigs: []anki.WriteDeckConfig{{ID: 30, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
						Notes: []anki.WriteNote{{ID: 40, GUID: "guid", NoteTypeID: 10, Fields: []string{"front"}}}, Cards: []anki.WriteCard{card}, Reviews: rows,
					}}
					var raw bytes.Buffer
					if err := anki.WriteArchive(&raw, &pkg); err != nil {
						t.Fatal(err)
					}
					report, err := store.NewDeckStore(db).ImportPackage(context.Background(), user.ID, &raw, store.PackageImportOptions{Target: store.PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
					if err != nil {
						t.Fatal(err)
					}
					var state store.CardState
					if err := db.Where("user_id = ?", user.ID).First(&state).Error; err != nil {
						t.Fatal(err)
					}
					var reviews []store.Review
					if err := db.Where("card_id = ? AND user_id = ?", state.CardID, user.ID).Order("id").Find(&reviews).Error; err != nil {
						t.Fatal(err)
					}
					if len(reviews) != len(rows) {
						t.Fatalf("reviews=%+v", reviews)
					}
					if reviews[0].Stability == nil || reviews[0].Difficulty == nil || reviews[1].Stability == nil || reviews[1].Difficulty == nil || *reviews[0].Stability != *reviews[1].Stability || *reviews[0].Difficulty != *reviews[1].Difficulty {
						t.Fatalf("interior cram does not preserve preceding memory: %+v", reviews)
					}
					if state.Stability == nil || state.Difficulty == nil || reviews[2].Stability == nil || reviews[2].Difficulty == nil || *reviews[2].Stability != *state.Stability || *reviews[2].Difficulty != *state.Difficulty {
						t.Fatalf("last selected does not preserve final memory: %+v", reviews[2])
					}
					for i := 3; i < len(reviews); i++ {
						if reviews[i].Stability == nil || reviews[i].Difficulty == nil || *reviews[i].Stability != *reviews[i-1].Stability || *reviews[i].Difficulty != *reviews[i-1].Difficulty {
							t.Fatalf("tail does not preserve preceding memory: %+v", reviews[i])
						}
					}
					var deck store.Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var preset store.Preset
					if err := db.First(&preset, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					s := mustScheduler(t, &preset)
					now := time.Unix(1700000000, 0)
					result := mustSubmit(t, db, SubmitInput{CardID: state.CardID, UserID: user.ID, Rating: Good, ExpectedVersion: state.Version, Scheduler: s, Now: now, Location: time.UTC})
					restored, err := undoOnce(t, db, state.CardID, user.ID, result.State.Version, s, now.Add(time.Second))
					if err != nil {
						t.Fatal(err)
					}
					if restored.Stability == nil || restored.Difficulty == nil || *restored.Stability != *state.Stability || *restored.Difficulty != *state.Difficulty || restored.State != state.State || restored.Reps != state.Reps || restored.Lapses != state.Lapses || restored.ScheduledDays != state.ScheduledDays || restored.DueAt == nil || state.DueAt == nil || !restored.DueAt.Equal(*state.DueAt) || restored.LastReviewAt == nil || state.LastReviewAt == nil || !restored.LastReviewAt.Equal(*state.LastReviewAt) {
						t.Fatalf("restored=%+v imported=%+v", restored, state)
					}
				})
			}
		}
	}
}

func TestUndoRefusesMissingMemoryWithoutWrites(t *testing.T) {
	for driver, db := range undoMemoryDatabases(t) {
		if err := db.Create(&store.User{Username: "guard", Email: "guard@example.com", DisplayName: "guard", Role: store.RoleUser, Status: store.StatusActive, Locale: "en", Timezone: "UTC"}).Error; err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name string
			prev bool
			s, d *float64
		}{
			{"no-previous", false, nil, nil}, {"missing-both", true, nil, nil}, {"missing-s", true, nil, store.Ptr(5.0)},
			{"missing-d", true, store.Ptr(5.0), nil}, {"zero-s", true, store.Ptr(0.0), store.Ptr(5.0)},
			{"zero-d", true, store.Ptr(5.0), store.Ptr(0.0)}, {"high-d", true, store.Ptr(5.0), store.Ptr(11.0)},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
				deck := seedDeck(t, db, now)
				card := seedCard(t, db, deck, "forward", now)
				state := store.CardState{CardID: card, UserID: 1, State: "review", Stability: store.Ptr(5.0), Difficulty: store.Ptr(5.0), Version: 1}
				if err := db.Create(&state).Error; err != nil {
					t.Fatal(err)
				}
				if tc.prev {
					r := store.Review{CardID: card, UserID: 1, Rating: 3, GradeSource: "self", ReviewedAt: now, ReviewDay: "2025-01-01", StateBefore: 0, Stability: tc.s, Difficulty: tc.d}
					if err := db.Create(&r).Error; err != nil {
						t.Fatal(err)
					}
				}
				r := store.Review{CardID: card, UserID: 1, Rating: 3, GradeSource: "self", ReviewedAt: now.Add(time.Hour), ReviewDay: "2025-01-01", StateBefore: 2}
				if err := db.Create(&r).Error; err != nil {
					t.Fatal(err)
				}
				type tables struct {
					States  []store.CardState
					Reviews []store.Review
					Audits  []store.AuditLog
				}
				read := func() tables {
					var v tables
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
				_, err := Rollback(context.Background(), tx, UndoInput{CardID: card, UserID: 1, ExpectedVersion: 1, Scheduler: mustScheduler(t, testPreset(t)), Now: now})
				if !errors.Is(err, ErrNothingToUndo) {
					t.Fatalf("error=%v", err)
				}
				if err := tx.Commit().Error; err != nil {
					t.Fatal(err)
				}
				after := read()
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("tables changed before=%+v after=%+v", before, after)
				}
			})
		}
	}
}

func TestUndoMemoryValidation(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0, -1} {
		if validUndoMemory(&v, store.Ptr(5.0)) || validUndoMemory(store.Ptr(5.0), &v) {
			t.Fatalf("accepted %v", v)
		}
	}
	if validUndoMemory(store.Ptr(5.0), store.Ptr(10.1)) || !validUndoMemory(store.Ptr(.001), store.Ptr(10.0)) {
		t.Fatal("incorrect boundary")
	}
}
