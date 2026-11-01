package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/anki"
	"git.nite07.com/nite/engram/internal/pgtest"
)

func TestAnkiProgressLocksExistingState(t *testing.T) {
	db, ok := pgtest.Open(t)
	if !ok {
		return
	}
	t.Run("postgres", func(t *testing.T) {
		if err := db.AutoMigrate(AllModels()...); err != nil {
			t.Fatal(err)
		}
		user := seedUsers(t, db, "progress_lock")[0]
		deck, notes := seedPackageDeck(t, db, user)
		var card Card
		if err := db.Where("note_id = ?", notes[0]).First(&card).Error; err != nil {
			t.Fatal(err)
		}
		local := time.Unix(1500000000, 0).UTC()
		if err := db.Model(&CardState{}).Where("card_id = ? AND user_id = ?", card.ID, user).Update("last_review_at", local).Error; err != nil {
			t.Fatal(err)
		}
		pkg := &anki.Package{CreatedAt: 1500000000, Cards: []anki.Card{{ID: 1, Type: 2, Queue: 2, Reps: 7, Data: `{"s":4.5,"d":6.2,"lrt":1600000000}`}}}
		result := make(chan *gorm.DB, 1)
		started := make(chan struct{})
		called := false
		if err := db.Callback().Query().After("gorm:query").Register("test:progress_lock", func(tx *gorm.DB) {
			if called || tx.Statement.Table != "card_states" {
				return
			}
			called = true
			states := tx.Statement.Dest.(*[]CardState)
			if len(*states) != 1 {
				t.Errorf("loaded states = %d", len(*states))
				return
			}
			version := (*states)[0].Version
			go func() {
				close(started)
				result <- db.Exec("UPDATE card_states SET version = version + 1 WHERE card_id = ? AND user_id = ? AND version = ?", card.ID, user, version)
			}()
			<-started
			time.Sleep(200 * time.Millisecond)
			select {
			case r := <-result:
				t.Errorf("update completed before import commit: rows=%d error=%v", r.RowsAffected, r.Error)
				result <- r
			default:
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer db.Callback().Query().Remove("test:progress_lock")
		report := &PackageImportReport{}
		if err := db.Transaction(func(tx *gorm.DB) error {
			return importAnkiProgress(context.Background(), tx, user, deck, pkg, map[int64]uint64{1: card.ID}, PackageImportOptions{IncludeProgress: true}, report, time.Now())
		}); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("state load callback was not called")
		}
		select {
		case r := <-result:
			if r.Error != nil || r.RowsAffected != 0 {
				t.Fatalf("update rows=%d error=%v", r.RowsAffected, r.Error)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("update did not complete after commit")
		}
		var state CardState
		if err := db.Where("card_id = ? AND user_id = ?", card.ID, user).First(&state).Error; err != nil {
			t.Fatal(err)
		}
		if state.Reps != 7 || state.Version != 3 || state.LastReviewAt == nil || state.LastReviewAt.Unix() != 1600000000 || report.ProgressApplied != 1 {
			t.Fatalf("state=%+v report=%+v", state, report)
		}
	})
}

func TestAnkiProgressConcurrentStateCreate(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "progress_create_"+driver)[0]
			deck, notes := seedPackageDeck(t, db, user)
			var card Card
			if err := db.Where("note_id = ?", notes[1]).First(&card).Error; err != nil {
				t.Fatal(err)
			}
			pkg := &anki.Package{CreatedAt: 1500000000, Cards: []anki.Card{{ID: 1, Type: 2, Queue: 2, Reps: 7, Data: `{"s":4.5,"d":6.2,"lrt":1600000000}`}}, Reviews: []anki.Review{{ID: 1600000000000, CardID: 1, Ease: 3, Type: 1}}}
			called := false
			if err := db.Callback().Query().After("gorm:query").Register("test:progress_create_race", func(tx *gorm.DB) {
				if called || tx.Statement.Table != "card_states" {
					return
				}
				called = true
				if len(*tx.Statement.Dest.(*[]CardState)) != 0 {
					t.Error("expected no loaded states")
				}
				if err := tx.Session(&gorm.Session{NewDB: true}).Create(&CardState{CardID: card.ID, UserID: user, State: "learning", Reps: 99, Version: 5}).Error; err != nil {
					tx.AddError(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer db.Callback().Query().Remove("test:progress_create_race")
			report := &PackageImportReport{}
			if err := db.Transaction(func(tx *gorm.DB) error {
				return importAnkiProgress(context.Background(), tx, user, deck, pkg, map[int64]uint64{1: card.ID}, PackageImportOptions{IncludeProgress: true}, report, time.Now())
			}); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("state load callback was not called")
			}
			var state CardState
			if err := db.Where("card_id = ? AND user_id = ?", card.ID, user).First(&state).Error; err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&Review{}).Where("card_id = ? AND user_id = ?", card.ID, user).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if state.State != "learning" || state.Reps != 99 || state.Version != 5 || count != 0 || report.StatesSkipped != 1 || report.ProgressApplied != 0 || report.ReviewsSkipped != 1 || report.ReviewsApplied != 0 {
				t.Fatalf("state=%+v reviews=%d report=%+v", state, count, report)
			}
		})
	}
}

func TestAnkiResetStateBefore(t *testing.T) {
	const start int64 = 1600000000000
	const day int64 = 86400000
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				reviews []anki.Review
				want    []int
			}{
				{"reset", []anki.Review{{ID: start, CardID: 1, Type: 0, Ease: 3}, {ID: start + day, CardID: 1, Type: 1, Ease: 3}, {ID: start + 200*day, CardID: 1, Type: 4, Ease: 0, Factor: 0}, {ID: start + 201*day, CardID: 1, Type: 0, Ease: 1}}, []int{0, 2, 0}},
				{"no_reset", []anki.Review{{ID: start, CardID: 1, Type: 0, Ease: 3}, {ID: start + day, CardID: 1, Type: 0, Ease: 1}}, []int{0, 1}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					user := seedUsers(t, db, "reset_"+driver+"_"+tc.name)[0]
					deck, notes := seedPackageDeck(t, db, user)
					var card Card
					if err := db.Where("note_id = ?", notes[0]).First(&card).Error; err != nil {
						t.Fatal(err)
					}
					pkg := &anki.Package{CreatedAt: 1600000000, Cards: []anki.Card{{ID: 1, Type: 1, Queue: 1, Due: 1600000000}}, Reviews: tc.reviews}
					if err := importAnkiProgress(context.Background(), db, user, deck, pkg, map[int64]uint64{1: card.ID}, PackageImportOptions{IncludeProgress: true}, &PackageImportReport{}, time.Now()); err != nil {
						t.Fatal(err)
					}
					var reviews []Review
					if err := db.Where("card_id = ? AND user_id = ?", card.ID, user).Order("reviewed_at").Find(&reviews).Error; err != nil {
						t.Fatal(err)
					}
					var got []int
					for _, r := range reviews {
						got = append(got, r.StateBefore)
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("state_before = %v, want %v", got, tc.want)
					}
				})
			}
		})
	}
}

func TestAnkiProgressApproximationWarn(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				firstType int
				noHistory bool
				wantCount int
			}{
				{name: "review_first", firstType: 1, wantCount: 2},
				{name: "relearning_first", firstType: 2, wantCount: 2},
				{name: "filtered_first", firstType: 3, wantCount: 2},
				{name: "learning_first", firstType: 0},
				{name: "no_history", noHistory: true, wantCount: 2},
			} {
				t.Run(tc.name, func(t *testing.T) {
					user := seedUsers(t, db, "approx_"+driver+"_"+tc.name)[0]
					deck, notes := seedPackageDeck(t, db, user)
					pkg := &anki.Package{CreatedAt: 1600000000}
					ids := make(map[int64]uint64)
					for i, noteID := range notes[:2] {
						var card Card
						if err := db.Where("note_id = ?", noteID).First(&card).Error; err != nil {
							t.Fatal(err)
						}
						ankiID := int64(i + 1)
						ids[ankiID] = card.ID
						pkg.Cards = append(pkg.Cards, anki.Card{ID: ankiID, Type: 2, Queue: 2, Factor: 2500, Interval: 10})
						if !tc.noHistory {
							pkg.Reviews = append(pkg.Reviews, anki.Review{ID: 1600000000000 + int64(i), CardID: ankiID, Ease: 3, Type: tc.firstType})
						}
					}
					var logs bytes.Buffer
					previous := slog.Default()
					slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
					defer slog.SetDefault(previous)
					if err := importAnkiProgress(context.Background(), db, user, deck, pkg, ids, PackageImportOptions{IncludeProgress: true}, &PackageImportReport{}, time.Now()); err != nil {
						t.Fatal(err)
					}
					if tc.wantCount == 0 {
						if logs.Len() != 0 {
							t.Fatalf("unexpected log: %s", logs.String())
						}
						return
					}
					var record map[string]any
					if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
						t.Fatalf("expected one log: %s: %v", logs.String(), err)
					}
					if record["level"] != "WARN" || record["msg"] != "Anki import memory approximation is not aligned with Anki" || fmt.Sprint(record["user_id"]) != fmt.Sprint(user) || record["cards"] != float64(tc.wantCount) {
						t.Fatalf("unexpected log: %v", record)
					}
					t.Logf("captured warning: %s", bytes.TrimSpace(logs.Bytes()))
				})
			}
		})
	}
}

func TestAnkiImportFilteredHomeDeck(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name      string
			decks     [][2]int64
			paths     []string
			retention float64
		}{
			{"filtered_home", [][2]int64{{99, 10}}, []string{""}, .85},
			{"filtered_subdeck", [][2]int64{{10, 0}, {99, 11}}, []string{"", "Sub"}, .85},
			{"unfiltered", [][2]int64{{10, 0}, {11, 0}}, []string{"", "Sub"}, .85},
			{"home_preset", [][2]int64{{99, 10}}, []string{""}, .85},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards = pkg.Notes[:len(tc.decks)], pkg.Cards[:len(tc.decks)]
				pkg.Decks = []anki.Deck{
					{ID: 10, Name: "Home", Description: "Home description", ConfigID: 20},
					{ID: 11, Name: "Home::Sub", ConfigID: 20},
					{ID: 99, Name: "Filtered", Description: "Filtered description", ConfigID: 21},
				}
				pkg.DeckConfigs = []anki.DeckConfig{
					{ID: 20, DesiredRetention: .85, LearnSteps: []float64{1}, RelearnSteps: []float64{10}, MaximumReviewInterval: 100},
					{ID: 21, DesiredRetention: .95, LearnSteps: []float64{2}, RelearnSteps: []float64{20}, MaximumReviewInterval: 200},
				}
				for i, ids := range tc.decks {
					pkg.Cards[i].DeckID, pkg.Cards[i].OriginalDeckID = ids[0], ids[1]
				}
				user := seedUsers(t, db, "filtered_home_"+driver+"_"+tc.name)[0]
				plan, report := planAnkiDecks(pkg), &PackageImportReport{}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, Now: time.Now}, report); err != nil {
					t.Fatal(err)
				}
				var deck Deck
				if err := db.First(&deck, report.DeckID).Error; err != nil {
					t.Fatal(err)
				}
				if deck.Name != "Home" || deck.Description != "Home description" {
					t.Fatalf("deck = %+v", deck)
				}
				var preset Preset
				if err := db.First(&preset, deck.PresetID).Error; err != nil {
					t.Fatal(err)
				}
				if preset.DesiredRetention != tc.retention {
					t.Fatalf("retention = %v, want %v", preset.DesiredRetention, tc.retention)
				}
				var cards []Card
				if err := db.Joins("JOIN notes ON notes.id = cards.note_id").Where("notes.deck_id = ?", deck.ID).Order("cards.id").Find(&cards).Error; err != nil {
					t.Fatal(err)
				}
				if len(cards) != len(tc.paths) {
					t.Fatalf("cards = %d, want %d", len(cards), len(tc.paths))
				}
				for i, want := range tc.paths {
					if want == "" && cards[i].DeckPath != nil || want != "" && (cards[i].DeckPath == nil || *cards[i].DeckPath != want) {
						t.Errorf("card %d path = %v, want %q", i, cards[i].DeckPath, want)
					}
				}
			})
		}
	}
}

func TestAnkiImportUnreadableRevlog(t *testing.T) {
	data := mutateAnkiFixture(t, "stock.anki2.apkg",
		fixtureSQL{query: "DELETE FROM revlog"},
		fixtureSQL{query: "INSERT INTO revlog (id, cid, usn, ease, ivl, lastIvl, factor, time, type) SELECT 1600000000000, id, 0, 3, 1, 0, 2500, 100, 1 FROM cards LIMIT 1"},
		fixtureSQL{query: "INSERT INTO revlog (id, cid, usn, ease, ivl, lastIvl, factor, time, type) SELECT 1600000000001, id, 0, 3, 'invalid', 0, 2500, 100, 1 FROM cards LIMIT 1"},
	)
	pkg, err := anki.Read(bytes.NewReader(data), int64(len(data)))
	if err != nil || pkg.RevlogErr == nil || len(pkg.Reviews) != 1 || len(pkg.Notes) == 0 || len(pkg.Cards) == 0 {
		t.Fatalf("fixture: pkg=%+v error=%v", pkg, err)
	}
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name    string
			enabled bool
		}{{"progress_on", true}, {"progress_off", false}} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := seedUsers(t, db, "unreadable_revlog_"+driver+"_"+tc.name)[0]
				models := []any{&Deck{}, &Note{}, &Card{}, &CardState{}, &Review{}}
				counts := make([]int64, len(models))
				for i, model := range models {
					if err := db.Model(model).Count(&counts[i]).Error; err != nil {
						t.Fatal(err)
					}
				}
				mediaRoot := t.TempDir()
				_, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(data), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: tc.enabled, MediaRoot: mediaRoot})
				if tc.enabled {
					var pe *PackageError
					if !errors.As(err, &pe) || pe.Code != CodePackageBadFormat {
						t.Fatalf("error = %v, want package_bad_format", err)
					}
					entries, err := os.ReadDir(mediaRoot)
					if err != nil || len(entries) != 0 {
						t.Fatalf("media entries = %v, error = %v", entries, err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				want := []int64{0, 0, 0, 0, 0}
				if !tc.enabled {
					want[0], want[1], want[2] = 1, int64(len(pkg.Notes)), int64(len(pkg.Cards))
				}
				deltas := make([]int64, len(models))
				for i, model := range models {
					var got int64
					if err := db.Model(model).Count(&got).Error; err != nil {
						t.Fatal(err)
					}
					deltas[i] = got - counts[i]
					if deltas[i] != want[i] {
						t.Errorf("%T: added %d, want %d", model, got-counts[i], want[i])
					}
				}
				t.Logf("progress=%t deck/note/card/state/review deltas=%v", tc.enabled, deltas)
			})
		}
	}
}

func TestAnkiProgressLocalWins(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name                                             string
			localOffset                                      int64
			applied, skipped, reviewsApplied, reviewsSkipped int
		}{
			{"local_newer", 2000, 0, 1, 0, 3},
			{"package_newer", -1000, 1, 0, 2, 1},
			{"equal", 0, 0, 1, 1, 2},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
				card := &pkg.Cards[0]
				const base int64 = 1600000000000
				card.Type, card.Queue, card.Data, card.Reps = 2, 2, `{"s":4.5,"d":6.2,"lrt":1600000000000}`, 7
				pkg.Reviews = []anki.Review{
					{ID: base - 1000, CardID: card.ID, Ease: 3, Type: 1},
					{ID: base, CardID: card.ID, Ease: 3, Type: 1},
					{ID: base + 1000, CardID: card.ID, Ease: 3, Type: 1},
				}
				user := seedUsers(t, db, "wins_"+driver+"_"+tc.name)[0]
				plan, initial := planAnkiDecks(pkg), &PackageImportReport{}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, Now: time.Now}, initial); err != nil {
					t.Fatal(err)
				}
				var row Card
				if err := db.Where("note_id IN (?)", db.Model(&Note{}).Select("id").Where("deck_id = ?", initial.DeckID)).First(&row).Error; err != nil {
					t.Fatal(err)
				}
				local := time.UnixMilli(base + tc.localOffset).UTC()
				if err := db.Create(&CardState{CardID: row.ID, UserID: user, State: "review", Reps: 42, LastReviewAt: &local}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&Review{CardID: row.ID, UserID: user, Rating: 2, GradeSource: "self", ReviewedAt: local, ReviewDay: "2020-09-13", StateBefore: 2}).Error; err != nil {
					t.Fatal(err)
				}
				report := &PackageImportReport{}
				if err := importAnkiProgress(context.Background(), db, user, initial.DeckID, pkg, map[int64]uint64{card.ID: row.ID}, PackageImportOptions{IncludeProgress: true}, report, time.Now()); err != nil {
					t.Fatal(err)
				}
				var state CardState
				if err := db.Where("card_id = ? AND user_id = ?", row.ID, user).First(&state).Error; err != nil {
					t.Fatal(err)
				}
				wantReps := 42
				if tc.applied == 1 {
					wantReps = 7
				}
				if state.Reps != wantReps || report.ProgressApplied != tc.applied || report.StatesSkipped != tc.skipped || report.ReviewsApplied != tc.reviewsApplied || report.ReviewsSkipped != tc.reviewsSkipped {
					t.Fatalf("state=%+v report=%+v", state, report)
				}
				t.Logf("state reps=%d applied=%d skipped=%d; reviews applied=%d skipped=%d", state.Reps, report.ProgressApplied, report.StatesSkipped, report.ReviewsApplied, report.ReviewsSkipped)
			})
		}
	}
}

func TestAnkiProgressPackageTime(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name     string
			existing bool
		}{
			{"local_review", true},
			{"no_existing_state", false},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards, pkg.Reviews = pkg.Notes[:1], pkg.Cards[:1], nil
				card := &pkg.Cards[0]
				card.Type, card.Queue, card.Due, card.Interval, card.Reps, card.Data = 1, 1, 1600000600, 0, 9, `{"s":3,"d":5}`
				user := seedUsers(t, db, "package_time_"+driver+"_"+tc.name)[0]
				plan, initial := planAnkiDecks(pkg), &PackageImportReport{}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, Now: time.Now}, initial); err != nil {
					t.Fatal(err)
				}
				var row Card
				if err := db.Where("note_id IN (?)", db.Model(&Note{}).Select("id").Where("deck_id = ?", initial.DeckID)).First(&row).Error; err != nil {
					t.Fatal(err)
				}
				var baseline CardState
				if tc.existing {
					local := time.Unix(1600000300, 0).UTC()
					state := CardState{CardID: row.ID, UserID: user, State: "learning", Reps: 42, LastReviewAt: &local}
					if err := db.Create(&state).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Where("card_id = ? AND user_id = ?", row.ID, user).First(&baseline).Error; err != nil {
						t.Fatal(err)
					}
				}
				for i := 0; i < 3; i++ {
					report := &PackageImportReport{}
					if err := importAnkiProgress(context.Background(), db, user, initial.DeckID, pkg, map[int64]uint64{card.ID: row.ID}, PackageImportOptions{IncludeProgress: true}, report, time.Now()); err != nil {
						t.Fatal(err)
					}
					var state CardState
					if err := db.Where("card_id = ? AND user_id = ?", row.ID, user).First(&state).Error; err != nil {
						t.Fatal(err)
					}
					if !tc.existing && i == 0 {
						if report.ProgressApplied != 1 || report.StatesSkipped != 0 || state.LastReviewAt != nil {
							t.Fatalf("first import state=%+v report=%+v", state, report)
						}
						baseline = state
					} else if report.ProgressApplied != 0 || report.StatesSkipped != 1 || !reflect.DeepEqual(state, baseline) {
						t.Fatalf("import %d state=%+v baseline=%+v report=%+v", i+1, state, baseline, report)
					}
					t.Logf("import %d: version=%d applied=%d skipped=%d", i+1, state.Version, report.ProgressApplied, report.StatesSkipped)
				}
			})
		}
	}
}

func TestAnkiProgressBatchStatements(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
			if err != nil {
				t.Fatal(err)
			}
			pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
			card := &pkg.Cards[0]
			card.Type, card.Queue, card.Data = 2, 2, `{"s":4.5,"d":6.2}`
			for i := 0; i < 5000; i++ {
				pkg.Reviews = append(pkg.Reviews, anki.Review{ID: 1600000000000 + int64(i)*86400000, CardID: card.ID, Ease: 3, Type: 1})
			}
			user := seedUsers(t, db, "batch_progress_"+driver)[0]
			plan, report := planAnkiDecks(pkg), &PackageImportReport{}
			if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, Now: time.Now}, report); err != nil {
				t.Fatal(err)
			}
			var row Card
			if err := db.First(&row).Error; err != nil {
				t.Fatal(err)
			}
			var note Note
			if err := db.First(&note, row.NoteID).Error; err != nil {
				t.Fatal(err)
			}
			count := 0
			callback := func(tx *gorm.DB) { count++ }
			if err := db.Callback().Query().Before("gorm:query").Register("test:progress_query", callback); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Create().Before("gorm:create").Register("test:progress_create", callback); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Update().Before("gorm:update").Register("test:progress_update", callback); err != nil {
				t.Fatal(err)
			}
			defer db.Callback().Query().Remove("test:progress_query")
			defer db.Callback().Create().Remove("test:progress_create")
			defer db.Callback().Update().Remove("test:progress_update")
			report = &PackageImportReport{}
			if err := importAnkiProgress(context.Background(), db, user, note.DeckID, pkg, map[int64]uint64{card.ID: row.ID}, PackageImportOptions{IncludeProgress: true}, report, time.Now()); err != nil {
				t.Fatal(err)
			}
			t.Logf("progress statements: %d; reviews applied: %d", count, report.ReviewsApplied)
			if count >= 100 || report.ReviewsApplied != 5000 {
				t.Fatalf("statements=%d report=%+v", count, report)
			}
		})
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver+"/many_skipped_notes", func(t *testing.T) {
			counts := make(map[int]map[string]int)
			for _, size := range []int{10, 1000} {
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				note, card := pkg.Notes[0], pkg.Cards[0]
				pkg.Notes, pkg.Cards, pkg.Reviews = nil, nil, nil
				for i := 0; i < size; i++ {
					n := note
					n.ID, n.GUID = int64(i+1), fmt.Sprintf("batch-%d", i)
					c := card
					c.ID, c.NoteID = int64(i+1), n.ID
					c.Type, c.Queue, c.Data = 2, 2, `{"s":4.5,"d":6.2}`
					pkg.Notes = append(pkg.Notes, n)
					pkg.Cards = append(pkg.Cards, c)
					pkg.Reviews = append(pkg.Reviews, anki.Review{ID: 1600000000000 + int64(i), CardID: c.ID, Ease: 3, Type: 1})
				}
				user := seedUsers(t, db, fmt.Sprintf("batch_many_%s_%d", driver, size))[0]
				plan, initial := planAnkiDecks(pkg), &PackageImportReport{}
				store := NewDeckStore(db)
				opts := PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, OnConflict: "skip", Now: time.Now}
				if err := store.importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, opts, initial); err != nil {
					t.Fatal(err)
				}
				if initial.NotesCreated != size || initial.CardsCreated != size || initial.ProgressApplied != size || initial.ReviewsApplied != size {
					t.Fatalf("initial import size=%d report=%+v", size, initial)
				}
				counts[size] = make(map[string]int)
				if err := db.Callback().Query().Before("gorm:query").Register("test:many_progress_query", func(tx *gorm.DB) {
					switch tx.Statement.Table {
					case "card_states", "reviews", "cards":
						counts[size][tx.Statement.Table]++
					}
				}); err != nil {
					t.Fatal(err)
				}
				report := &PackageImportReport{}
				err = store.importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, "into_deck", initial.DeckID, opts, report)
				if removeErr := db.Callback().Query().Remove("test:many_progress_query"); removeErr != nil {
					t.Fatal(removeErr)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("cards=%d SELECT counts=%v report notes_skipped=%d states_skipped=%d reviews_skipped=%d", size, counts[size], report.NotesSkipped, report.StatesSkipped, report.ReviewsSkipped)
				if report.NotesSkipped != size || report.NotesCreated != 0 || report.StatesSkipped != size || report.ReviewsSkipped != size {
					t.Fatalf("repeat import size=%d report=%+v", size, report)
				}
				if counts[size]["card_states"]+counts[size]["reviews"] > 4 {
					t.Errorf("size=%d card_states and reviews SELECTs=%d, want at most 4", size, counts[size]["card_states"]+counts[size]["reviews"])
				}
			}
			if counts[1000]["cards"] > counts[10]["cards"]+2 {
				t.Errorf("cards SELECTs: 1000 cards=%d, 10 cards=%d; want at most 2 more", counts[1000]["cards"], counts[10]["cards"])
			}
		})
	}
}

func TestAnkiDiscardedProgressCounts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cards     []anki.Card
		reviews   []anki.Review
		states    int
		discarded bool
	}{
		{name: "all_new", cards: []anki.Card{{}, {}}, states: 0},
		{name: "review", cards: []anki.Card{{}, {Type: 2, Queue: 2}}, states: 1, discarded: true},
		{name: "queue_only", cards: []anki.Card{{Queue: -1}}, states: 1, discarded: true},
		{name: "reviews_only", reviews: []anki.Review{{}}, discarded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &PackageImportReport{}
			pkg := &anki.Package{Cards: tc.cards, Reviews: tc.reviews}
			if err := importAnkiProgress(context.Background(), nil, 0, 0, pkg, nil, PackageImportOptions{}, report, time.Time{}); err != nil {
				t.Fatal(err)
			}
			if report.StatesDiscarded != tc.states || report.ReviewsDiscarded != len(tc.reviews) || report.ProgressDiscarded != tc.discarded {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestAnkiReviewColumns(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
			if err != nil {
				t.Fatal(err)
			}
			pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
			card := &pkg.Cards[0]
			card.Type, card.Queue, card.Data = 2, 2, `{}`
			base := int64(1600000000000)
			pkg.Reviews = []anki.Review{
				{ID: base, CardID: card.ID, Ease: 3, Type: 0, Interval: -600},
				{ID: base + 86400000, CardID: card.ID, Ease: 3, Type: 1, Interval: 4},
				{ID: base + 86400000 + 43200000, CardID: card.ID, Ease: 1, Type: 2, Interval: 0},
			}
			user := seedUsers(t, db, "columns_"+driver)[0]
			plan, report := planAnkiDecks(pkg), &PackageImportReport{}
			if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: time.Now}, report); err != nil {
				t.Fatal(err)
			}
			var reviews []Review
			if err := db.Where("user_id = ?", user).Order("reviewed_at").Find(&reviews).Error; err != nil {
				t.Fatal(err)
			}
			if len(reviews) != 3 {
				t.Fatalf("reviews: %+v", reviews)
			}
			var state CardState
			if err := db.Where("user_id = ?", user).First(&state).Error; err != nil {
				t.Fatal(err)
			}
			engine := fsrs.NewFSRS(fsrs.DefaultParam())
			historical, err := engine.HistoricalMemoryStates(fsrs.ReviewEntries{{Rating: 3}, {Rating: 3, DeltaT: 1}, {Rating: 1, DeltaT: 1}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i, tc := range []struct {
				interval float64
				duration *float64
			}{{600.0 / 86400, nil}, {4, Ptr(1.0)}, {0, Ptr(.5)}} {
				r := reviews[i]
				want := historical[i]
				if i == 2 {
					want = fsrs.MemoryState{Stability: *state.Stability, Difficulty: *state.Difficulty}
				}
				if r.IntervalDays == nil || *r.IntervalDays != tc.interval || (r.DurationDays == nil) != (tc.duration == nil) || (tc.duration != nil && *r.DurationDays != *tc.duration) || r.Stability == nil || r.Difficulty == nil || math.Abs(*r.Stability-want.Stability) > 1e-9 || math.Abs(*r.Difficulty-want.Difficulty) > 1e-9 {
					t.Errorf("row %d: %+v want interval=%v duration=%v memory=%+v", i, r, tc.interval, tc.duration, want)
				}
			}
		})
	}
}

func TestAnkiImportDueDates(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name                                 string
			kind, queue                          int
			due, originalDue, originalDeck, want int64
		}{
			{"learning_queue1", 1, 1, 1700000000, 0, 0, 1700000000},
			{"relearning_queue1", 3, 1, 1700000000, 0, 0, 1700000000},
			{"learning_queue3", 1, 3, 3, 0, 0, 1500259200},
			{"relearning_queue3", 3, 3, 3, 0, 0, 1500259200},
			{"learning_suspended_interday", 1, -1, 3, 0, 0, 1500259200},
			{"learning_buried_interday", 1, -2, 3, 0, 0, 1500259200},
			{"relearning_buried_interday", 3, -3, 3, 0, 0, 1500259200},
			{"filtered_learning_interday", 1, 2, -100, 3, 1, 1500259200},
			{"learning_suspended_intraday", 1, -1, 1700000000, 0, 0, 1700000000},
			{"filtered_learning_queue1", 1, 1, -100, 1700000000, 1, 1700000000},
			{"filtered_relearning_queue1", 3, 1, -100, 1700000000, 1, 1700000000},
			{"filtered_learning_queue3", 1, 3, -100, 3, 1, 1500259200},
			{"filtered_review", 2, 2, -100, 3, 1, 1500259200},
			{"filtered_relearning_queue3", 3, 3, -100, 3, 1, 1500259200},
			{"unfiltered_ignores_odue", 2, 2, 3, 99, 0, 1500259200},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
				pkg.CreatedAt = 1500000000
				c := &pkg.Cards[0]
				c.Type, c.Queue, c.Due, c.OriginalDue, c.OriginalDeckID = tc.kind, tc.queue, tc.due, tc.originalDue, tc.originalDeck
				c.Data = `{"s":4.5,"d":6.2}`
				user := seedUsers(t, db, "due_"+driver+"_"+tc.name)[0]
				plan, report := planAnkiDecks(pkg), &PackageImportReport{}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: time.Now}, report); err != nil {
					t.Fatal(err)
				}
				var state CardState
				if err := db.Where("user_id = ?", user).First(&state).Error; err != nil {
					t.Fatal(err)
				}
				if state.DueAt == nil || state.DueAt.Unix() != tc.want {
					t.Fatalf("due=%v want=%d", state.DueAt, tc.want)
				}
			})
		}
	}
}

func TestAnkiLearningDueOraclePackage(t *testing.T) {
	name := ankiFixturePath("learning-due.anki21.apkg")
	pkg, err := anki.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Cards map[string]struct {
			Type, Queue          int
			Due, Odue, Odid, Crt int64
			DueAt                *int64 `json:"due_at"`
		} `json:"cards"`
	}
	rawGolden, err := os.ReadFile(ankiFixturePath("learning-due.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawGolden, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Cards) != 18 || len(pkg.Cards) != 18 {
		t.Fatalf("golden=%d cards=%d", len(golden.Cards), len(pkg.Cards))
	}
	labels := make(map[int64]string)
	for _, note := range pkg.Notes {
		labels[note.ID] = note.Fields[0]
	}
	for _, card := range pkg.Cards {
		want, ok := golden.Cards[labels[card.NoteID]]
		if !ok || pkg.CreatedAt != want.Crt || card.Type != want.Type || card.Queue != want.Queue || card.Due != want.Due || card.OriginalDue != want.Odue || card.OriginalDeckID != want.Odid {
			t.Fatalf("card=%+v golden=%+v", card, want)
		}
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			user := seedUsers(t, db, "filtered_oracle_"+driver)[0]
			raw, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			report, err := NewDeckStore(db).ImportPackage(context.Background(), user, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: true, MediaRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if report.ProgressApplied != len(golden.Cards) {
				t.Fatalf("report=%+v", report)
			}
			for label, want := range golden.Cards {
				var state CardState
				err := db.Table("card_states").Select("card_states.*").Joins("JOIN cards ON cards.id = card_states.card_id").Joins("JOIN notes ON notes.id = cards.note_id").Where("card_states.user_id = ? AND notes.fields_json LIKE ?", user, "%\""+label+"\"%").Take(&state).Error
				if err != nil {
					t.Fatalf("%s: %v", label, err)
				}
				due := want.Due
				if want.Odid != 0 {
					due = want.Odue
				}
				var expected int64
				if want.Type != 2 && due > 1000000000 {
					if want.DueAt == nil {
						t.Fatalf("%s: missing Anki due timestamp", label)
					}
					expected = *want.DueAt
				} else {
					// Anki's API anchors day numbers to the importing collection's current day.
					if want.DueAt != nil {
						t.Fatalf("%s: unexpected Anki due timestamp", label)
					}
					expected = want.Crt + due*86400
				}
				if state.DueAt == nil || state.DueAt.Unix() != expected {
					t.Errorf("%s due=%v want=%d", label, state.DueAt, expected)
				}
				t.Logf("%s due=%v want=%d", label, state.DueAt, expected)
			}
		})
	}
}

func TestAnkiProgressReplayStart(t *testing.T) {
	var golden struct {
		Cards map[string]struct {
			Stability  float64 `json:"stability"`
			Difficulty float64 `json:"difficulty"`
		} `json:"cards"`
	}
	raw, err := os.ReadFile(ankiFixturePath("memory-replay.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name, key string
			index     int
			learning  bool
		}{
			{"first_learning", "reset", 1, true},
			{"partial_review", "partial", 0, false},
			{"reset", "reset", 1, false},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				pkg, err := anki.ReadFile(ankiFixturePath("memory-replay.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				card := pkg.Cards[tc.index]
				pkg.Cards = []anki.Card{card}
				var reviews []anki.Review
				for _, r := range pkg.Reviews {
					if r.CardID == card.ID && (!tc.learning || r.ID > 1700000000000+210*86400000) {
						reviews = append(reviews, r)
					}
				}
				pkg.Reviews = reviews
				user := seedUsers(t, db, "replay_start_"+driver+"_"+tc.name)[0]
				plan, report := planAnkiDecks(pkg), &PackageImportReport{}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: time.Now}, report); err != nil {
					t.Fatal(err)
				}
				var state CardState
				if err := db.Where("user_id = ?", user).First(&state).Error; err != nil {
					t.Fatal(err)
				}
				want := golden.Cards[tc.key]
				// Anki's ComputedMemoryState is float32 and its Rust implementation rounds
				// differently; the observed 2–8 ULP differences are precision, not algorithm.
				if state.Stability == nil || state.Difficulty == nil || math.Abs(*state.Stability-want.Stability) > 1e-4 || math.Abs(*state.Difficulty-want.Difficulty) > 1e-4 {
					t.Fatalf("state=%+v want=%+v", state, want)
				}
			})
		}
	}
}

func TestAnkiProgressImport(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprint(enabled), func(t *testing.T) {
					ctx := context.Background()
					user := seedUsers(t, db, fmt.Sprintf("progress_%s_%t", driver, enabled))[0]
					pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
					if err != nil {
						t.Fatal(err)
					}
					pkg.Notes = pkg.Notes[:2]
					pkg.Cards = pkg.Cards[:2]
					pkg.DeckConfigs[0].DesiredRetention = .85
					pkg.DeckConfigs[0].LearnSteps = []float64{2, 12}
					pkg.DeckConfigs[0].RelearnSteps = []float64{3}
					pkg.DeckConfigs[0].MaximumReviewInterval = 123
					pkg.DeckConfigs[0].Weights = []float64{1, 2, 3}
					pkg.Cards[0].Type, pkg.Cards[0].Queue, pkg.Cards[0].Due = 2, 2, 5
					pkg.Cards[0].Interval, pkg.Cards[0].Reps, pkg.Cards[0].Lapses = 7, 3, 1
					pkg.Cards[0].Data = `{"s":4.5,"d":6.2,"lrt":1600000000000}`
					pkg.Cards[1].Queue = -1
					pkg.Cards[1].Interval = -600
					pkg.Cards[1].Data = `{"lrt":1600000000}`
					cid, newID := pkg.Cards[0].ID, pkg.Cards[1].ID
					pkg.Reviews = []anki.Review{
						{ID: 1600000000001, CardID: cid, Ease: 3, Interval: 7, Time: 1200, Type: 1},
						{ID: 1600000000002, CardID: newID, Ease: 4, Interval: -600, Time: 500, Type: 0},
						{ID: 1600000000003, CardID: cid, Ease: 0, Type: 0},
						{ID: 1600000000004, CardID: cid, Ease: 5, Type: 0},
						{ID: 1600000000005, CardID: cid, Ease: 3, Type: 4},
						{ID: 1600000000006, CardID: cid, Ease: 3, Type: 5},
						{ID: 1600000000007, CardID: 99999, Ease: 3, Type: 1},
					}
					now := time.Unix(1700000000, 0).UTC()
					opts := PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: enabled, ApplyWeights: true, OnConflict: "update", Now: func() time.Time { return now }}
					plan := planAnkiDecks(pkg)
					report := &PackageImportReport{}
					s := NewDeckStore(db)
					if err := s.importAnkiInTx(ctx, db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, opts, report); err != nil {
						t.Fatal(err)
					}
					var deck Deck
					if err := db.First(&deck, report.DeckID).Error; err != nil {
						t.Fatal(err)
					}
					var preset Preset
					if err := db.First(&preset, deck.PresetID).Error; err != nil {
						t.Fatal(err)
					}
					if preset.DesiredRetention != .85 || preset.LearningSteps != "2m,12m" || preset.RelearningSteps != "3m" || preset.MaximumIntervalDays != 123 || preset.WeightsJSON != nil || !report.WeightsDiscarded || report.WeightsApplied || preset.WeightsReviewCount != nil || preset.WeightsOptimizedAt != nil {
						t.Fatalf("preset: %+v", preset)
					}
					var states []CardState
					var reviews []Review
					if err := db.Where("user_id = ?", user).Order("card_id").Find(&states).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Where("user_id = ?", user).Order("reviewed_at").Find(&reviews).Error; err != nil {
						t.Fatal(err)
					}
					if !enabled {
						if len(states) != 0 || len(reviews) != 0 || !report.ProgressDiscarded || report.StatesDiscarded != 2 || report.ReviewsDiscarded != 7 {
							t.Fatalf("off: %+v %+v %+v", states, reviews, report)
						}
						return
					}
					if len(states) != 2 || len(reviews) != 2 || report.ProgressApplied != 2 || report.ReviewsApplied != 2 || report.ReviewsSkipped != 5 {
						t.Fatalf("on: %+v %+v %+v", states, reviews, report)
					}
					first, second := states[0], states[1]
					if first.State != "review" || first.Stability == nil || *first.Stability != 4.5 || first.Difficulty == nil || *first.Difficulty != 6.2 || first.LastReviewAt == nil || first.LastReviewAt.UnixMilli() != 1600000000000 || first.DueAt == nil || first.DueAt.Unix() != pkg.CreatedAt+5*86400 || first.Reps != 3 || first.ScheduledDays != 7 {
						t.Fatalf("review state: %+v", first)
					}
					if second.State != "new" || second.ScheduledDays != 0 || second.SuspendedAt == nil || !second.SuspendedAt.Equal(now) || second.LastReviewAt == nil || second.LastReviewAt.Unix() != 1600000000 {
						t.Fatalf("suspended state: %+v", second)
					}
					if reviews[0].StateBefore != 2 || reviews[0].GradeSource != "self" || reviews[0].ElapsedMS == nil || *reviews[0].ElapsedMS != 1200 || reviews[0].DurationDays != nil || reviews[0].IntervalDays == nil || *reviews[0].IntervalDays != 7 || reviews[0].PublicID == "" || reviews[1].StateBefore != 0 || reviews[1].DurationDays != nil || reviews[1].Rating != 4 || reviews[1].ElapsedMS == nil || *reviews[1].ElapsedMS != 500 || reviews[1].ReviewedAt.UnixMilli() != 1600000000002 || reviews[1].GradeSource != "self" {
						t.Fatalf("reviews: %+v", reviews)
					}
					again := &PackageImportReport{}
					if err := s.importAnkiInTx(ctx, db, user, pkg, nil, nil, &plan, "into_deck", report.DeckID, opts, again); err != nil {
						t.Fatal(err)
					}
					if again.ProgressApplied != 0 || again.StatesSkipped != 2 || again.ReviewsApplied != 0 || again.ReviewsSkipped != 7 {
						t.Fatalf("repeat: %+v", again)
					}
					var repeatedStates []CardState
					if err := db.Where("user_id = ?", user).Order("card_id").Find(&repeatedStates).Error; err != nil {
						t.Fatal(err)
					}
					if len(repeatedStates) != len(states) {
						t.Fatalf("repeat state count: got %d, want %d", len(repeatedStates), len(states))
					}
					for i := range states {
						if repeatedStates[i].CardID != states[i].CardID || repeatedStates[i].Version != states[i].Version {
							t.Errorf("repeat state %d: card/version got %d/%d, want %d/%d", i, repeatedStates[i].CardID, repeatedStates[i].Version, states[i].CardID, states[i].Version)
						}
					}
				})
			}
		})
	}
}

func TestAnkiProgressLastReview(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name           string
			kind, interval int
			data           string
			reviews        []anki.Review
			want           *int64
		}{
			{"lrt wins", 2, 7, `{"s":4.5,"d":6.2,"lrt":1600000000}`, []anki.Review{{ID: 1600086400123, Ease: 3, Type: 1}}, Ptr(int64(1600000000000))},
			{"kept revlog", 2, 7, `{"s":4.5,"d":6.2}`, []anki.Review{{ID: 1600000000000, Ease: 3, Type: 1}, {ID: 1600000001000, Type: 4}, {ID: 1600000002123, Ease: 3, Type: 1}, {ID: 1600000003000, Ease: 0, Type: 1}}, Ptr(int64(1600000002123))},
			{"review due minus days", 2, 7, `{"s":4.5,"d":6.2}`, nil, nil},
			{"relearning due minus seconds", 3, -600, `{"s":4.5,"d":6.2}`, nil, Ptr(int64(1699999400000))},
			{"learning without history", 1, -600, `{"s":4.5,"d":6.2}`, nil, nil},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := seedUsers(t, db, fmt.Sprintf("last_review_%s_%s", driver, tc.name))[0]
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
				card := &pkg.Cards[0]
				card.Type, card.Queue, card.Interval, card.Data = tc.kind, tc.kind, tc.interval, tc.data
				if tc.kind == 3 {
					card.Queue = 1
				}
				card.Due = 1700000000
				if tc.kind == 2 {
					card.Due = 10
				}
				pkg.Reviews = append([]anki.Review(nil), tc.reviews...)
				for i := range pkg.Reviews {
					pkg.Reviews[i].CardID = card.ID
				}
				plan, report := planAnkiDecks(pkg), &PackageImportReport{}
				opts := PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: time.Now}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, opts, report); err != nil {
					t.Fatal(err)
				}
				var state CardState
				if err := db.Where("user_id = ?", user).First(&state).Error; err != nil {
					t.Fatal(err)
				}
				want := tc.want
				if tc.name == "review due minus days" {
					want = Ptr((pkg.CreatedAt + 3*86400) * 1000)
				}
				if want == nil && state.LastReviewAt != nil || want != nil && (state.LastReviewAt == nil || state.LastReviewAt.UnixMilli() != *want) {
					t.Fatalf("last review = %v, want milliseconds %v", state.LastReviewAt, want)
				}
			})
		}
	}
}

func TestAnkiProgressMissingMemory(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name                   string
			kind, factor, interval int
			history, skipped       bool
			data                   string
		}{
			{"review history", 2, 2500, 7, true, false, `{}`},
			{"learning history", 1, 0, -600, true, false, `{}`},
			{"review SM2", 2, 2500, 7, false, false, `{}`},
			{"learning invalid SM2", 1, 0, -600, false, true, `{}`},
			{"relearning history", 3, 2500, 7, true, false, `{}`},
			{"relearning SM2", 3, 2500, 7, false, false, `{}`},
			{"review stability only", 2, 2500, 7, true, false, `{"s":5}`},
			{"review difficulty only", 2, 2500, 7, true, false, `{"d":5}`},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := seedUsers(t, db, fmt.Sprintf("memory_%s_%s", driver, tc.name))[0]
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
				card := &pkg.Cards[0]
				card.Type, card.Queue, card.Factor, card.Interval, card.Data = tc.kind, tc.kind, tc.factor, tc.interval, tc.data
				pkg.Reviews = nil
				if tc.history {
					pkg.Reviews = []anki.Review{{ID: 1600000000000, CardID: card.ID, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: card.ID, Ease: 4, Type: 1}}
				}
				report := &PackageImportReport{}
				plan := planAnkiDecks(pkg)
				opts := PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: time.Now}
				if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, opts, report); err != nil {
					t.Fatal(err)
				}
				var states []CardState
				if err := db.Where("user_id = ?", user).Find(&states).Error; err != nil {
					t.Fatal(err)
				}
				if tc.skipped {
					if len(states) != 0 || report.StatesSkipped != 1 {
						t.Fatalf("states=%+v report=%+v", states, report)
					}
					return
				}
				if len(states) != 1 || states[0].Stability == nil || states[0].Difficulty == nil {
					t.Fatalf("states=%+v report=%+v", states, report)
				}
				engine := fsrs.NewFSRS(fsrs.DefaultParam())
				var want *fsrs.MemoryState
				if tc.history {
					entries := make(fsrs.ReviewEntries, 0, len(pkg.Reviews))
					for i, review := range pkg.Reviews {
						entry := fsrs.ReviewEntry{Rating: fsrs.Rating(review.Ease)}
						if i > 0 {
							entry.DeltaT = float64((review.ID - pkg.Reviews[i-1].ID) / 86400000)
						}
						entries = append(entries, entry)
					}
					want, err = engine.MemoryState(entries, nil)
				} else {
					interval := float64(tc.interval)
					if interval < 0 {
						interval = -interval / 86400
					}
					want, err = engine.MemoryStateFromSM2(float64(tc.factor)/1000, interval, 0.9)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !(math.Abs(*states[0].Stability-want.Stability) <= 1e-4) || !(math.Abs(*states[0].Difficulty-want.Difficulty) <= 1e-4) {
					t.Fatalf("got stability=%g difficulty=%g, want stability=%g difficulty=%g", *states[0].Stability, *states[0].Difficulty, want.Stability, want.Difficulty)
				}
			})
		}
	}
}

func TestAnkiProgressCarriedMemoryValidation(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name, data string
			valid      bool
		}{
			{"zero_stability", `{"s":0,"d":5}`, false},
			{"zero_difficulty", `{"s":5,"d":0}`, false},
			{"negative_stability", `{"s":-1,"d":5}`, false},
			{"high_difficulty", `{"s":5,"d":11}`, false},
			{"valid", `{"s":5,"d":5}`, true},
		} {
			for _, history := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/history_%t", driver, tc.name, history), func(t *testing.T) {
					user := seedUsers(t, db, fmt.Sprintf("carried_%s_%s_%t", driver, tc.name, history))[0]
					pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
					if err != nil {
						t.Fatal(err)
					}
					pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
					card := &pkg.Cards[0]
					card.Type, card.Queue, card.Factor, card.Interval, card.Data = 2, 2, 0, 7, tc.data
					pkg.Reviews = nil
					if history {
						pkg.Reviews = []anki.Review{{ID: 1600000000000, CardID: card.ID, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: card.ID, Ease: 4, Type: 1}}
					}
					plan, report := planAnkiDecks(pkg), &PackageImportReport{}
					if err := NewDeckStore(db).importAnkiInTx(context.Background(), db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, Now: time.Now}, report); err != nil {
						t.Fatal(err)
					}
					var states []CardState
					if err := db.Where("user_id = ?", user).Find(&states).Error; err != nil {
						t.Fatal(err)
					}
					if !tc.valid && !history {
						if len(states) != 0 || report.StatesSkipped != 1 || report.ProgressApplied != 0 {
							t.Fatalf("states=%+v report=%+v", states, report)
						}
						return
					}
					wantS, wantD := 5.0, 5.0
					if !tc.valid {
						memory, err := fsrs.NewFSRS(fsrs.DefaultParam()).MemoryState(fsrs.ReviewEntries{{Rating: fsrs.Good}, {Rating: fsrs.Easy, DeltaT: 1}}, nil)
						if err != nil {
							t.Fatal(err)
						}
						wantS, wantD = memory.Stability, memory.Difficulty
					}
					if len(states) != 1 || states[0].Stability == nil || states[0].Difficulty == nil || *states[0].Stability != wantS || *states[0].Difficulty != wantD || report.ProgressApplied != 1 || report.StatesSkipped != 0 {
						t.Fatalf("states=%+v report=%+v want s=%g d=%g", states, report, wantS, wantD)
					}
				})
			}
		}
	}
}

func TestUsableAnkiMemory(t *testing.T) {
	for _, tc := range []struct {
		name string
		s, d float64
		want bool
	}{
		{"minimum", .001, 1, true}, {"maximum_difficulty", 5, 10, true},
		{"nan_stability", math.NaN(), 5, false}, {"infinite_stability", math.Inf(1), 5, false},
		{"nan_difficulty", 5, math.NaN(), false}, {"infinite_difficulty", 5, math.Inf(-1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := usableAnkiMemory(&tc.s, &tc.d); got != tc.want {
				t.Fatalf("usable = %t, want %t", got, tc.want)
			}
		})
	}
	if usableAnkiMemory(nil, nil) {
		t.Fatal("missing memory is usable")
	}
}

func TestAnkiProgressReplayUsesStudyWeightsAndReviewDays(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			user := seedUsers(t, db, "replay_"+driver)[0]
			if err := db.Model(&User{}).Where("id = ?", user).Updates(map[string]any{"timezone": "UTC", "day_cutoff_hour": 4}).Error; err != nil {
				t.Fatal(err)
			}
			pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
			if err != nil {
				t.Fatal(err)
			}
			pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
			card := &pkg.Cards[0]
			card.Type, card.Queue, card.Data = 2, 2, `{}`
			first := time.Date(2024, 3, 1, 3, 59, 0, 0, time.UTC)
			pkg.Reviews = []anki.Review{{ID: first.UnixMilli(), CardID: card.ID, Ease: 3, Type: 0}, {ID: first.Add(time.Minute).UnixMilli(), CardID: card.ID, Ease: 4, Type: 1}, {ID: first.Add(2 * time.Minute).UnixMilli(), CardID: card.ID, Ease: 0, Type: 1}}
			weights := fsrs.DefaultWeights()
			weights[2] += 2
			pkg.DeckConfigs[0].Weights = append([]float64(nil), weights[:]...)
			plan := planAnkiDecks(pkg)
			report := &PackageImportReport{}
			if err := NewDeckStore(db).importAnkiInTx(ctx, db, user, pkg, nil, nil, &plan, PackageTargetNewDeck, 0, PackageImportOptions{PresetMode: PackagePresetNew, IncludeProgress: true, ApplyWeights: true, Now: time.Now}, report); err != nil {
				t.Fatal(err)
			}
			params := fsrs.DefaultParam()
			params.W = weights
			want, err := fsrs.NewFSRS(params).MemoryState(fsrs.ReviewEntries{{Rating: fsrs.Good}, {Rating: fsrs.Easy, DeltaT: 1}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got CardState
			if err := db.Where("user_id = ?", user).First(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got.Stability == nil || got.Difficulty == nil || *got.Stability != want.Stability || *got.Difficulty != want.Difficulty {
				t.Fatalf("got %+v want %+v", got, want)
			}
		})
	}
}

func TestAnkiProgressReplayUsesEditorPreset(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			users := seedUsers(t, db, "replay_owner_"+driver, "replay_editor_"+driver)
			owner, editor := users[0], users[1]
			ownerPreset := NewPreset(owner, "owner")
			if err := db.Create(&ownerPreset).Error; err != nil {
				t.Fatal(err)
			}
			deck := Deck{OwnerUserID: owner, Name: "shared", PresetID: ownerPreset.ID}
			if err := db.Create(&deck).Error; err != nil {
				t.Fatal(err)
			}
			if err := NewGrantStore(db).Grant(ctx, deck.ID, editor, RoleEditor, &owner); err != nil {
				t.Fatal(err)
			}
			weights := fsrs.DefaultWeights()
			weights[2] += 2
			encoded, err := json.Marshal(weights)
			if err != nil {
				t.Fatal(err)
			}
			memberPreset := NewPreset(editor, "member")
			memberPreset.WeightsJSON = Ptr(string(encoded))
			if err := db.Create(&memberPreset).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&DeckMemberSetting{}).Where("deck_id = ? AND user_id = ?", deck.ID, editor).Update("preset_id", memberPreset.ID).Error; err != nil {
				t.Fatal(err)
			}
			pkg := anki.WritePackage{Collection: anki.WriteCollection{
				CreatedAt:   1600000000,
				NoteTypes:   []anki.WriteNoteType{{ID: 10, Name: "Basic", Fields: []anki.Field{{Name: "Front"}}, Templates: []anki.Template{{Name: "Card", QFormat: "{{Front}}", AFormat: "{{Front}}"}}}},
				Decks:       []anki.WriteDeck{{ID: 20, Name: "Imported", ConfigID: 30}},
				DeckConfigs: []anki.WriteDeckConfig{{ID: 30, Name: "Default", DesiredRetention: .9, LearningSteps: []float64{1, 10}, RelearningSteps: []float64{10}, MaximumIntervalDays: 36500}},
				Notes:       []anki.WriteNote{{ID: 40, GUID: "guid", NoteTypeID: 10, Fields: []string{"front"}}},
				Cards:       []anki.WriteCard{{ID: 50, NoteID: 40, DeckID: 20, Type: 2, Queue: 2, Ivl: 7, Factor: 2500}},
				Reviews:     []anki.WriteReview{{ID: 1600000000000, CardID: 50, Ease: 3, Type: 0}, {ID: 1600086400000, CardID: 50, Ease: 4, Type: 1}},
			}}
			var raw bytes.Buffer
			if err := anki.WriteArchive(&raw, &pkg); err != nil {
				t.Fatal(err)
			}
			if _, err := NewDeckStore(db).ImportPackage(ctx, editor, &raw, PackageImportOptions{Target: fmt.Sprintf("into_deck:%d", deck.ID), IncludeProgress: true, MediaRoot: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			entries := fsrs.ReviewEntries{{Rating: fsrs.Good}, {Rating: fsrs.Easy, DeltaT: 1}}
			params := fsrs.DefaultParam()
			ownerMemory, err := fsrs.NewFSRS(params).MemoryState(entries, nil)
			if err != nil {
				t.Fatal(err)
			}
			params.W = weights
			memberMemory, err := fsrs.NewFSRS(params).MemoryState(entries, nil)
			if err != nil {
				t.Fatal(err)
			}
			var got CardState
			if err := db.Where("user_id = ?", editor).First(&got).Error; err != nil {
				t.Fatal(err)
			}
			if got.Stability == nil || got.Difficulty == nil || *got.Stability != memberMemory.Stability || *got.Difficulty != memberMemory.Difficulty {
				t.Fatalf("got %+v, member memory %+v", got, memberMemory)
			}
			if *got.Stability == ownerMemory.Stability && *got.Difficulty == ownerMemory.Difficulty {
				t.Fatalf("got owner memory %+v", ownerMemory)
			}
		})
	}
}
