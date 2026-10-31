package store

import (
	"context"
	"math"
	"testing"

	"git.nite07.com/nite/engram/internal/anki"
)

func TestCreateAnkiPresetSchedulerLimits(t *testing.T) {
	cases := []struct {
		name                         string
		learning, relearning         []float64
		max                          int
		wantLearning, wantRelearning string
		wantMax                      int
	}{
		{"maximum", []float64{2}, []float64{3}, 50000, "2m", "3m", 36500},
		{"negative learning", []float64{2, -1}, []float64{3}, 1000, DefaultLearningSteps, "3m", 1000},
		{"nan learning", []float64{math.NaN()}, []float64{3}, 1000, DefaultLearningSteps, "3m", 1000},
		{"infinite relearning", []float64{2}, []float64{math.Inf(1)}, 1000, "2m", DefaultRelearningSteps, 1000},
		{"negative relearning", []float64{2}, []float64{-1}, 1000, "2m", DefaultRelearningSteps, 1000},
		{"empty steps", nil, nil, 36500, "", "", 36500},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_preset_limits")[0]
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cfg := &anki.DeckConfig{LearnSteps: tc.learning, RelearnSteps: tc.relearning, MaximumReviewInterval: tc.max}
					p, err := createAnkiPreset(context.Background(), db, owner, tc.name, cfg, false, &PackageImportReport{})
					if err != nil {
						t.Fatal(err)
					}
					got, err := NewPresetStore(db).ByID(context.Background(), p.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.LearningSteps != tc.wantLearning || got.RelearningSteps != tc.wantRelearning || got.MaximumIntervalDays != tc.wantMax {
						t.Fatalf("stored preset: %+v", got)
					}
				})
			}
		})
	}
}
