package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/apkg"
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
		{"learning over the step cap", []float64{36501 * 24 * 60}, []float64{3}, 1000, DefaultLearningSteps, "3m", 1000},
		{"empty steps", nil, nil, 36500, "", "", 36500},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_preset_limits")[0]
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cfg := &apkg.DeckConfig{LearnSteps: tc.learning, RelearnSteps: tc.relearning, MaximumReviewInterval: tc.max}
					p, err := createPreset(context.Background(), db, owner, tc.name, cfg, false, &PackageImportReport{})
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

func TestCreateAnkiPresetNonFiniteWeights(t *testing.T) {
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_nonfinite_weights")[0]
			for _, tc := range []struct {
				name  string
				value float64
			}{{"nan", math.NaN()}, {"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)}} {
				t.Run(tc.name, func(t *testing.T) {
					weights := packageTestWeights()
					weights[0] = tc.value
					report := &PackageImportReport{}
					preset, err := createPreset(context.Background(), db, owner, tc.name, &apkg.DeckConfig{Weights: weights}, true, report)
					if err != nil {
						t.Fatal(err)
					}
					if preset.WeightsJSON != nil || report.WeightsApplied || !report.WeightsInvalid {
						t.Fatalf("preset=%+v report=%+v", preset, report)
					}
					stored, err := NewPresetStore(db).ByID(context.Background(), preset.ID)
					if err != nil {
						t.Fatal(err)
					}
					if stored.WeightsJSON != nil {
						t.Fatalf("stored weights: %v", *stored.WeightsJSON)
					}
				})
			}
		})
	}
}

func TestAnkiImportWeightsInvalid(t *testing.T) {
	cases := []struct {
		name    string
		weights []float64
		apply   bool
		target  string
		invalid bool
	}{
		{"refused", []float64{1, 2, 3}, true, PackageTargetNewDeck, true},
		{"option off", []float64{1, 2, 3}, false, PackageTargetNewDeck, false},
		{"accepted", packageTestWeights(), true, PackageTargetNewDeck, false},
		{"absent", nil, true, PackageTargetNewDeck, false},
		{"existing deck", []float64{1, 2, 3}, true, "into_deck", false},
	}
	for driver, db := range packageDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			owner := seedUsers(t, db, "anki_invalid_weights")[0]
			pkg, err := apkg.ReadFile(ankiFixturePath("stock.anki2.apkg"))
			if err != nil {
				t.Fatal(err)
			}
			plan := planImportDecks(pkg)
			s := NewDeckStore(db)
			opts := PackageImportOptions{PresetMode: PackagePresetNew, Now: time.Now}
			deckID, err := s.resolveTargetDeck(context.Background(), db, owner, pkg, &plan, PackageTargetNewDeck, 0, opts, &PackageImportReport{})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					pkg.DeckConfigs[0].Weights = tc.weights
					opts.ApplyWeights = tc.apply
					report := &PackageImportReport{}
					var logs bytes.Buffer
					previous := slog.Default()
					slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
					defer slog.SetDefault(previous)
					_, err := s.resolveTargetDeck(context.Background(), db, owner, pkg, &plan, tc.target, deckID, opts, report)
					if err != nil {
						t.Fatal(err)
					}
					if report.WeightsInvalid != tc.invalid {
						t.Fatalf("weights_invalid = %t, want %t", report.WeightsInvalid, tc.invalid)
					}
					if !tc.invalid {
						if logs.Len() != 0 {
							t.Fatalf("unexpected log: %s", logs.String())
						}
						return
					}
					var record map[string]any
					if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); err != nil {
						t.Fatalf("expected one log: %s: %v", logs.String(), err)
					}
					if record["level"] != "WARN" || fmt.Sprint(record["user_id"]) != fmt.Sprint(owner) || record["error"] == nil || record["error"] == "" {
						t.Fatalf("unexpected log: %v", record)
					}
				})
			}
		})
	}
}
