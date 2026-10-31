package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/anki"
)

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
					cid, newID := pkg.Cards[0].ID, pkg.Cards[1].ID
					pkg.Reviews = []anki.Review{
						{ID: 1600000000001, CardID: cid, Ease: 3, Interval: 7, Time: 1200, Type: 1},
						{ID: 1600000000002, CardID: newID, Ease: 4, Interval: 1, Time: 500, Type: 0},
						{ID: 1600000000003, CardID: cid, Ease: 0, Type: 0},
						{ID: 1600000000004, CardID: cid, Ease: 5, Type: 0},
						{ID: 1600000000005, CardID: cid, Ease: 3, Type: 4},
						{ID: 1600000000006, CardID: cid, Ease: 3, Type: 5},
						{ID: 1600000000007, CardID: 99999, Ease: 3, Type: 1},
					}
					now := time.Unix(1700000000, 0).UTC()
					opts := PackageImportOptions{IncludeProgress: enabled, OnConflict: "update", Now: func() time.Time { return now }}
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
					if preset.DesiredRetention != .85 || preset.LearningSteps != "2m,12m" || preset.RelearningSteps != "3m" || preset.MaximumIntervalDays != 123 || preset.WeightsJSON == nil || *preset.WeightsJSON != "[1,2,3]" || preset.WeightsReviewCount != nil || preset.WeightsOptimizedAt != nil {
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
					if second.State != "new" || second.SuspendedAt == nil || !second.SuspendedAt.Equal(now) {
						t.Fatalf("suspended state: %+v", second)
					}
					if reviews[0].StateBefore != 2 || reviews[0].GradeSource != "self" || reviews[0].ElapsedMS == nil || *reviews[0].ElapsedMS != 1200 || reviews[0].DurationDays == nil || *reviews[0].DurationDays != 7 || reviews[0].PublicID == "" || reviews[1].StateBefore != 0 {
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
