package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// optimiseSampleWeights returns a 21-dimensional weight vector shaped like the real optimizer's; non-integer values make sure a round-trip comparison
// is not masked by floating-point rounding.
func optimiseSampleWeights() []float64 {
	w := make([]float64, 21)
	for i := range w {
		w[i] = float64(i) + 0.5
	}
	return w
}

// TestOptimisePresetWeightsRoundTrip is the store-level case for the "weight round trip": once the 21-dimensional weights
// the optimizer produced are written into a preset, reading them back must match element by element, and the optimization time and the review count are saved with them.
// This is the data-side contract of the chain "optimization done -> persisted -> page/scheduler read the same weights".
func TestOptimisePresetWeightsRoundTrip(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owner := seedUsers(t, db, "optimise_roundtrip")[0]
			presets := NewPresetStore(db)

			p := NewPreset(owner, "RoundTrip")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			weights := optimiseSampleWeights()
			raw, err := json.Marshal(weights)
			if err != nil {
				t.Fatalf("marshal weights: %v", err)
			}
			encoded := string(raw)
			optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			reviewCount := 512
			p.WeightsJSON = &encoded
			p.WeightsOptimizedAt = &optimizedAt
			p.WeightsReviewCount = &reviewCount
			if err := presets.Update(ctx, owner, &p); err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			got, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.WeightsJSON == nil {
				t.Fatal("weights_json is nil after a successful optimize write")
			}
			var decoded []float64
			if err := json.Unmarshal([]byte(*got.WeightsJSON), &decoded); err != nil {
				t.Fatalf("decode weights_json %q: %v", *got.WeightsJSON, err)
			}
			if len(decoded) != 21 {
				t.Fatalf("weights_json has %d elements, want 21", len(decoded))
			}
			for i, want := range weights {
				if decoded[i] != want {
					t.Errorf("weights[%d] = %v, want %v", i, decoded[i], want)
				}
			}
			if got.WeightsOptimizedAt == nil || !got.WeightsOptimizedAt.Equal(optimizedAt) {
				t.Errorf("weights_optimized_at = %v, want %v", got.WeightsOptimizedAt, optimizedAt)
			}
			if got.WeightsReviewCount == nil || *got.WeightsReviewCount != reviewCount {
				t.Errorf("weights_review_count = %v, want %d", got.WeightsReviewCount, reviewCount)
			}
		})
	}
}

// TestOptimiseResetWeightsClearsPreset is the "rollback" case, covering the backend capability the spec names:
// after a one-click rollback to the default weights, the weights_json / weights_optimized_at / weights_review_count columns
// all become NULL together (clearing weights_json alone leaves the illusion of "optimized"), and idempotency plus owner authorization are verified.
func TestOptimiseResetWeightsClearsPreset(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			owners := seedUsers(t, db, "optimise_reset_owner", "optimise_reset_intruder")
			owner, intruder := owners[0], owners[1]
			presets := NewPresetStore(db)

			p := NewPreset(owner, "Reset")
			if err := presets.Create(ctx, &p); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			weights := `[0.5,1.5,2.5,3.5,4.5,5.5,6.5,0.05,1.0,0.2,0.8,1.5,0.06,0.3,1.6,0.6,1.9,0.5,0.1,0.07,0.15]`
			optimizedAt := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
			reviewCount := 700
			p.WeightsJSON = &weights
			p.WeightsOptimizedAt = &optimizedAt
			p.WeightsReviewCount = &reviewCount
			if err := presets.Update(ctx, owner, &p); err != nil {
				t.Fatalf("Update(optimized weights) error = %v", err)
			}

			// A non-owner cannot roll back (same authorization rule as Update).
			if err := presets.ResetPresetWeights(ctx, intruder, p.ID); !errors.Is(err, ErrNotOwner) {
				t.Fatalf("ResetPresetWeights(intruder) error = %v, want ErrNotOwner", err)
			}

			if err := presets.ResetPresetWeights(ctx, owner, p.ID); err != nil {
				t.Fatalf("ResetPresetWeights(owner) error = %v", err)
			}
			got, err := presets.ByID(ctx, p.ID)
			if err != nil {
				t.Fatalf("ByID() error = %v", err)
			}
			if got.WeightsJSON != nil {
				t.Errorf("weights_json = %q after revert, want NULL", *got.WeightsJSON)
			}
			if got.WeightsOptimizedAt != nil {
				t.Errorf("weights_optimized_at = %v after revert, want NULL", got.WeightsOptimizedAt)
			}
			if got.WeightsReviewCount != nil {
				t.Errorf("weights_review_count = %d after revert, want NULL", *got.WeightsReviewCount)
			}

			// Idempotent: rolling back a preset that is already at the default succeeds again; a repeated click is not an error.
			if err := presets.ResetPresetWeights(ctx, owner, p.ID); err != nil {
				t.Errorf("ResetPresetWeights(second call) error = %v, want nil", err)
			}

			// A missing primary key yields the sentinel error.
			if err := presets.ResetPresetWeights(ctx, owner, 0); !errors.Is(err, ErrPresetWeightsIDRequired) {
				t.Errorf("ResetPresetWeights(id=0) error = %v, want ErrPresetWeightsIDRequired", err)
			}
		})
	}
}

// TestOptimizeMinReviewsFloor is acceptance 1: the four situations the threshold read path faces with stored values.
// A stored value below the floor (say 100, possibly from a direct database write or an old version) is clamped to 300, not reset to the default 500;
// 300, exactly at the floor, and 500, above it, take effect as they are; unset / empty / non-numeric / non-positive integers still fall back to the default 500
// (that is "the value is unusable", a different semantics from clamping "a legal but too low value").
func TestOptimizeMinReviewsFloor(t *testing.T) {
	// The floor must be below the default, otherwise "clamp to the floor" and "fall back to the default" are indistinguishable and this test is meaningless.
	if MinOptimizeMinReviews >= DefaultOptimizeMinReviews {
		t.Fatalf("MinOptimizeMinReviews = %d, want < DefaultOptimizeMinReviews = %d", MinOptimizeMinReviews, DefaultOptimizeMinReviews)
	}
	cases := []struct {
		name  string
		value string
		set   bool
		want  int
	}{
		{"stale value below the floor clamps to the floor", "100", true, MinOptimizeMinReviews},
		{"the floor itself passes through", "300", true, MinOptimizeMinReviews},
		{"a value above the floor passes through", "500", true, 500},
		{"unset setting falls back to the default", "", false, DefaultOptimizeMinReviews},
		{"empty value falls back to the default", "", true, DefaultOptimizeMinReviews},
		{"non-numeric value falls back to the default", "abc", true, DefaultOptimizeMinReviews},
		{"zero falls back to the default", "0", true, DefaultOptimizeMinReviews},
	}
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if err := db.Where("key = ?", SettingKeyOptimizeMinReviews).Delete(&Setting{}).Error; err != nil {
						t.Fatalf("clear setting: %v", err)
					}
					if tc.set {
						if err := PutSetting(ctx, db, SettingKeyOptimizeMinReviews, tc.value, nil, time.Now().UTC()); err != nil {
							t.Fatalf("PutSetting(%q): %v", tc.value, err)
						}
					}
					got, err := OptimizeMinReviews(ctx, db)
					if err != nil {
						t.Fatalf("OptimizeMinReviews: %v", err)
					}
					if got != tc.want {
						t.Errorf("OptimizeMinReviews() with stored %q (set=%v) = %d, want %d", tc.value, tc.set, got, tc.want)
					}
					t.Logf("stored %q -> effective %d (want %d)", tc.value, got, tc.want)
				})
			}
		})
	}
}
