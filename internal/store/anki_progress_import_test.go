package store

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/anki"
)

// TestRealAnkiPackage optionally exercises an externally generated Anki export.
func TestRealAnkiPackage(t *testing.T) {
	name := os.Getenv("TEST_ANKI_PACKAGE")
	if name == "" {
		t.Skip("TEST_ANKI_PACKAGE not set")
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			ctx := context.Background()
			user := seedUsers(t, db, "real_anki")[0]
			report, err := NewDeckStore(db).ImportPackage(ctx, user, bytes.NewReader(raw), PackageImportOptions{Target: PackageTargetNewDeck, IncludeProgress: true, ApplyWeights: true, MediaRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			var states []CardState
			var reviews []Review
			if err := db.Where("user_id = ? AND reps > 0", user).Find(&states).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Where("user_id = ?", user).Order("reviewed_at").Find(&reviews).Error; err != nil {
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
			if len(states) != 1 || len(reviews) < 2 || states[0].Stability == nil || states[0].Difficulty == nil || states[0].LastReviewAt == nil || states[0].LastReviewAt.Year() < 2020 || preset.WeightsJSON == nil {
				t.Fatalf("states=%+v reviews=%+v preset=%+v", states, reviews, preset)
			}
			for _, s := range states {
				t.Logf("state state=%s due=%v stability=%g difficulty=%g reps=%d lapses=%d last_review=%s", s.State, s.DueAt, *s.Stability, *s.Difficulty, s.Reps, s.Lapses, s.LastReviewAt.Format(time.RFC3339))
			}
			for _, r := range reviews {
				duration := "NULL"
				if r.DurationDays != nil {
					duration = fmt.Sprint(*r.DurationDays)
				}
				t.Logf("review rating=%d reviewed_at=%s elapsed_ms=%d duration_days=%s state_before=%d review_day=%s", r.Rating, r.ReviewedAt.Format(time.RFC3339Nano), *r.ElapsedMS, duration, r.StateBefore, r.ReviewDay)
			}
			t.Logf("preset retention=%g learning=%s relearning=%s maximum_interval=%d weights=%s", preset.DesiredRetention, preset.LearningSteps, preset.RelearningSteps, preset.MaximumIntervalDays, *preset.WeightsJSON)
		})
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
					if reviews[0].StateBefore != 2 || reviews[0].GradeSource != "self" || reviews[0].ElapsedMS == nil || *reviews[0].ElapsedMS != 1200 || reviews[0].DurationDays == nil || *reviews[0].DurationDays != 7 || reviews[0].PublicID == "" || reviews[1].StateBefore != 0 || reviews[1].DurationDays != nil || reviews[1].Rating != 4 || reviews[1].ElapsedMS == nil || *reviews[1].ElapsedMS != 500 || reviews[1].ReviewedAt.UnixMilli() != 1600000000002 || reviews[1].GradeSource != "self" {
						t.Fatalf("reviews: %+v", reviews)
					}
					again := &PackageImportReport{}
					if err := s.importAnkiInTx(ctx, db, user, pkg, nil, nil, &plan, "into_deck", report.DeckID, opts, again); err != nil {
						t.Fatal(err)
					}
					if again.ProgressApplied != 0 || again.StatesSkipped != 2 || again.ReviewsApplied != 0 || again.ReviewsSkipped != 7 {
						t.Fatalf("repeat: %+v", again)
					}
				})
			}
		})
	}
}

func TestAnkiProgressMissingMemory(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		for _, tc := range []struct {
			name                   string
			kind, factor, interval int
			history, skipped       bool
		}{
			{"review history", 2, 2500, 7, true, false},
			{"learning history", 1, 0, -600, true, false},
			{"review SM2", 2, 2500, 7, false, false},
			{"learning invalid SM2", 1, 0, -600, false, true},
		} {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				user := seedUsers(t, db, fmt.Sprintf("memory_%s_%d_%d_%t", driver, tc.kind, tc.factor, tc.history))[0]
				pkg, err := anki.ReadFile(ankiFixturePath("stock.anki2.apkg"))
				if err != nil {
					t.Fatal(err)
				}
				pkg.Notes, pkg.Cards = pkg.Notes[:1], pkg.Cards[:1]
				card := &pkg.Cards[0]
				card.Type, card.Queue, card.Factor, card.Interval, card.Data = tc.kind, tc.kind, tc.factor, tc.interval, `{}`
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
				if len(states) != 1 || states[0].Stability == nil || *states[0].Stability < .001 || states[0].Difficulty == nil || *states[0].Difficulty < 1 || *states[0].Difficulty > 10 {
					t.Fatalf("states=%+v report=%+v", states, report)
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
