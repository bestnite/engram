package schedule

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/store"
)

// allRecalledLog builds a fixed review log: cards cards with days reviews each (consecutive days, all Good),
// plus one extra card with a single review at the end. Everything is recalled, so the weight set with "higher predicted probability" necessarily fits better —
// which makes "the better weight set" known in advance, letting acceptance case 1 assert the direction of Improved().
func allRecalledLog(cards, days int) []store.OptimizerReviewLog {
	base := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	var logs []store.OptimizerReviewLog
	for card := 1; card <= cards; card++ {
		for d := 0; d < days; d++ {
			at := base.AddDate(0, 0, d).Add(time.Duration(card) * time.Minute)
			logs = append(logs, store.OptimizerReviewLog{
				CardID:       uint64(card),
				ReviewTime:   at.UnixMilli(),
				ReviewRating: int(fsrs.Good),
				ReviewState:  int(fsrs.Review),
				Timezone:     "UTC",
			})
		}
	}
	// A card with only one review: there is no prior memory state, so it must contribute 0 predictable items.
	at := base.Add(time.Hour)
	logs = append(logs, store.OptimizerReviewLog{
		CardID:       uint64(cards + 1),
		ReviewTime:   at.UnixMilli(),
		ReviewRating: int(fsrs.Good),
		ReviewState:  int(fsrs.New),
		Timezone:     "UTC",
	})
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].ReviewTime < logs[j].ReviewTime })
	return logs
}

// fitTestPreset builds a deterministic preset with no learning steps and fuzz off, with weights supplied by jsonWeights.
func fitTestPreset(t *testing.T, jsonWeights *string) *store.Preset {
	t.Helper()
	fuzz := false
	return &store.Preset{
		DesiredRetention:    0.9,
		LearningSteps:       "",
		RelearningSteps:     "",
		MaximumIntervalDays: 36500,
		EnableFuzz:          &fuzz,
		WeightsJSON:         jsonWeights,
	}
}

func jsonWeights(t *testing.T, w fsrs.Weights) *string {
	t.Helper()
	b, err := json.Marshal(w[:])
	if err != nil {
		t.Fatalf("marshal weights: %v", err)
	}
	s := string(b)
	return &s
}

// betterWeights returns a "clearly better" weight set: the default weights on an "everything recalled" log
// give a fairly high predicted probability, so their fit should beat the worseWeights below.
func betterWeights() []float64 {
	w := fsrs.DefaultWeights()
	return w[:]
}

// worseWeights clamps the initial stability weight to its lower bound, so predicted probabilities are systematically low, and on an everything-recalled log
// it necessarily produces a larger log loss — it is the known "worse" weight set.
func worseWeights() fsrs.Weights {
	w := fsrs.DefaultWeights()
	for i := 0; i < 4; i++ {
		w[i] = 0.001
	}
	return w
}

// TestCompareFitKnownBetterWeights is acceptance case 1: with a fixed review log and a known better weight set,
// it asserts the direction (Improved() is true when the worse weights come first and the better weights second), and prints both actual values.
func TestCompareFitKnownBetterWeights(t *testing.T) {
	const cards, days = 3, 6
	logs := allRecalledLog(cards, days)
	preset := fitTestPreset(t, jsonWeights(t, worseWeights()))

	before, after, err := CompareFit(preset, betterWeights(), logs)
	if err != nil {
		t.Fatalf("CompareFit: %v", err)
	}
	if !(after.LogLoss < before.LogLoss) {
		t.Fatalf("better weights must fit better: LogLoss before(worse)=%.6f after(better)=%.6f", before.LogLoss, after.LogLoss)
	}
	result := store.OptimizeResult{
		FitBefore: store.FitMetrics{LogLoss: before.LogLoss, RMSE: before.RMSE},
		FitAfter:  store.FitMetrics{LogLoss: after.LogLoss, RMSE: after.RMSE},
	}
	if !result.Improved() {
		t.Fatalf("Improved() = false, want true (after %.6f < before %.6f)", after.LogLoss, before.LogLoss)
	}
	t.Logf("fixed log: cards=%d days=%d (+1 single-review card)", cards, days)
	t.Logf("WORSE weights: LogLoss=%.6f RMSE=%.6f items=%d", before.LogLoss, before.RMSE, before.Items)
	t.Logf("BETTER weights: LogLoss=%.6f RMSE=%.6f items=%d", after.LogLoss, after.RMSE, after.Items)
	t.Logf("Improved()=%v", result.Improved())
}

// TestCompareFitSameItemCount is acceptance case 2/3: both metrics cover the same number of items,
// and the first review of each card is excluded (a card with only one review contributes 0 items).
func TestCompareFitSameItemCount(t *testing.T) {
	const cards, days = 3, 6
	logs := allRecalledLog(cards, days)
	preset := fitTestPreset(t, jsonWeights(t, worseWeights()))

	before, after, err := CompareFit(preset, betterWeights(), logs)
	if err != nil {
		t.Fatalf("CompareFit: %v", err)
	}
	want := cards * (days - 1) // every card drops its first review; a single-review card contributes 0
	if before.Items != after.Items {
		t.Fatalf("item count mismatch: before=%d after=%d", before.Items, after.Items)
	}
	if before.Items != want {
		t.Fatalf("items = %d, want %d (first review of every card excluded)", before.Items, want)
	}
	t.Logf("items: before=%d after=%d want=%d", before.Items, after.Items, want)
}

// TestCompareFitNoPredictableItems is acceptance case 5 (data side): when there are no predictable items at all,
// both metrics are the zero value and Available() is false, so the page never claims "not improved".
func TestCompareFitNoPredictableItems(t *testing.T) {
	preset := fitTestPreset(t, jsonWeights(t, worseWeights()))
	// Every card has only one review → all are excluded by the first-review rule.
	logs := allRecalledLog(2, 1)
	before, after, err := CompareFit(preset, betterWeights(), logs)
	if err != nil {
		t.Fatalf("CompareFit: %v", err)
	}
	if before.Items != 0 || after.Items != 0 {
		t.Fatalf("items = %d/%d, want 0/0", before.Items, after.Items)
	}
	result := store.OptimizeResult{
		FitBefore: store.FitMetrics{LogLoss: before.LogLoss, RMSE: before.RMSE},
		FitAfter:  store.FitMetrics{LogLoss: after.LogLoss, RMSE: after.RMSE},
	}
	if result.FitBefore.Available() || result.FitAfter.Available() {
		t.Fatalf("metrics reported available with no predictable items")
	}
	t.Logf("no predictable items: before.available=%v after.available=%v Improved()=%v",
		result.FitBefore.Available(), result.FitAfter.Available(), result.Improved())
}
