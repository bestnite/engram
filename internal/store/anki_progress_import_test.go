package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/anki"
)

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

func TestAnkiFilteredOraclePackage(t *testing.T) {
	name := os.Getenv("TEST_ANKI_FILTERED_PACKAGE")
	if name == "" {
		t.Skip("TEST_ANKI_FILTERED_PACKAGE not set")
	}
	pkg, err := anki.ReadFile(name)
	if err != nil {
		t.Fatal(err)
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
			var states []CardState
			if err := db.Where("user_id = ?", user).Order("state").Find(&states).Error; err != nil {
				t.Fatal(err)
			}
			if len(states) != 3 {
				t.Fatalf("states=%+v report=%+v", states, report)
			}
			for _, state := range states {
				want := pkg.CreatedAt + 3*86400
				if state.DueAt == nil || state.DueAt.Unix() != want {
					t.Fatalf("state=%s due=%v want=%d", state.State, state.DueAt, want)
				}
				t.Logf("state=%s due=%s unix=%d", state.State, state.DueAt.UTC().Format(time.RFC3339), state.DueAt.Unix())
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
