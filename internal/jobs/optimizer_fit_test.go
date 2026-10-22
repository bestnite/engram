//go:build unix

// Compiles only on Unix: it depends on the unix-only test scaffolding in jobs_test.go (newTestRunner and others),
// or uses `/bin/sh` directly as the fake command.

package jobs

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// degradedWeights pushes the initial stability weights down to their floor, creating a set of old weights clearly different from the adapter output,
// to prove that in the end-to-end run before reads the preset as it was before the write-back, rather than the new weights being copied to both sides.
func degradedWeights() fsrs.Weights {
	w := fsrs.DefaultWeights()
	for i := 0; i < 4; i++ {
		w[i] = 0.001
	}
	return w
}

// setPresetWeightsForTest writes weights straight into the preset row, simulating "this preset already had a set of old weights before optimization".
func setPresetWeightsForTest(db *gorm.DB, presetID uint64, w fsrs.Weights) error {
	raw, err := json.Marshal(w[:])
	if err != nil {
		return err
	}
	return db.Model(&store.Preset{}).Where("id = ?", presetID).Update("weights_json", string(raw)).Error
}

// TestOptimizeEndToEndComputesFitMetrics is acceptance 4: after an end-to-end job through the real adapter finishes,
// both the before and after fit metrics on the job row must be non-zero (they used to be always zero, so the page always showed "not improved").
// The preset is pre-seeded with old weights different from the adapter output, so before/after must differ — which also proves the old weights
// were read before FinishOptimize's write-back, rather than the new weights being copied to both sides. The adapter is built on this machine, so it really runs instead of skipping.
func TestOptimizeEndToEndComputesFitMetrics(t *testing.T) {
	bin := optimizerBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("optimizer binary not built at %s; run `cargo build --release` in tools/optimizer", bin)
	}
	ctx := context.Background()
	db, st := newWiringDB(t)
	u, p := seedOptimizerUserAndPreset(t, db)
	// 8 cards × 40 days = 320 reviews: this both clears the 300-review threshold floor and keeps the predictable item count
	// (320 - one first review per card = 312) comfortably above MinFitItems, so the end-to-end run can give a real verdict.
	const cards, days = 8, 40
	seedAdapterReviews(t, db, u.ID, p.ID, cards, days)
	// Give the preset old weights different from the adapter output: this both proves "the old weights are read before the write-back"
	// and makes the after/before metrics necessarily differ (rather than one evaluation copied to both sides).
	if err := setPresetWeightsForTest(db, p.ID, degradedWeights()); err != nil {
		t.Fatalf("seed stale preset weights: %v", err)
	}
	// The threshold is set to its floor: a stored value below the floor is clamped to 300, while the default 500 would block these 320 reviews.
	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, strconv.Itoa(store.MinOptimizeMinReviews), nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	opt, err := NewOptimizer(OptimizerDeps{DB: db, Binary: bin})
	if err != nil {
		t.Fatalf("NewOptimizer: %v", err)
	}
	runner := newWiringRunner(t, db, st, opt, 2*time.Minute)

	job, err := runner.EnqueueOptimize(ctx, u.ID, p.ID)
	if err != nil {
		t.Fatalf("EnqueueOptimize: %v", err)
	}
	done := waitForStatus(t, st, job.ID, StatusSucceeded, 2*time.Minute)
	if done.ResultJSON == nil {
		t.Fatalf("job %d succeeded without result_json", done.ID)
	}

	var result store.OptimizeResult
	if err := json.Unmarshal([]byte(*done.ResultJSON), &result); err != nil {
		t.Fatalf("decode result_json %q: %v", *done.ResultJSON, err)
	}
	if result.FitBefore.LogLoss <= 0 || result.FitBefore.RMSE <= 0 {
		t.Fatalf("FitBefore must be non-zero after a real run: %+v", result.FitBefore)
	}
	if result.FitAfter.LogLoss <= 0 || result.FitAfter.RMSE <= 0 {
		t.Fatalf("FitAfter must be non-zero after a real run: %+v", result.FitAfter)
	}
	if !result.FitBefore.Available() || !result.FitAfter.Available() {
		t.Fatalf("metrics reported unavailable after a real run: before=%+v after=%+v", result.FitBefore, result.FitAfter)
	}
	// The item count must land in result_json along with the metrics (fitMetrics previously only carried LogLoss/RMSE),
	// and this sample batch must be large enough to decide "improved / not improved".
	if result.FitBefore.Items <= 0 || result.FitAfter.Items <= 0 {
		t.Fatalf("fit metrics carried no item count after a real run: before=%+v after=%+v", result.FitBefore, result.FitAfter)
	}
	if result.FitBefore.Items != result.FitAfter.Items {
		t.Errorf("before/after item counts differ: %d vs %d (they must score the same item set)", result.FitBefore.Items, result.FitAfter.Items)
	}
	if !result.FitBefore.SampleSufficient() || !result.FitAfter.SampleSufficient() {
		t.Fatalf("real run produced fewer than %d items (before=%d after=%d); the sample gate would hide the verdict", store.MinFitItems, result.FitBefore.Items, result.FitAfter.Items)
	}
	if result.FitBefore == result.FitAfter {
		// The old weights (pre-write-back) and the new weights land on the same metric, meaning both sides actually evaluated the same weight set.
		t.Fatalf("before/after metrics identical; the pre-optimization weights were not used: %+v", result.FitBefore)
	}
	t.Logf("Improved()=%v (direction is asserted in acceptance 1 with a fixed log)", result.Improved())
	// After the job ends the preset row holds the adapter's new weights; the old weights no longer exist, proving before was read pre-write-back.
	reloaded, err := store.NewPresetStore(db).ByID(ctx, p.ID)
	if err != nil {
		t.Fatalf("reload preset: %v", err)
	}
	if reloaded.WeightsJSON == nil {
		t.Fatalf("preset %d has no weights_json after optimize", p.ID)
	}
	var stored []float64
	if err := json.Unmarshal([]byte(*reloaded.WeightsJSON), &stored); err != nil {
		t.Fatalf("decode stored weights: %v", err)
	}
	for i, w := range stored {
		if w != result.Weights[i] {
			t.Fatalf("stored weights[%d]=%v != adapter output %v", i, w, result.Weights[i])
		}
	}
	t.Logf("job %d succeeded (reviews_used=%d, weights=%d)", done.ID, result.ReviewsUsed, len(result.Weights))
	t.Logf("FitBefore (degraded old weights): LogLoss=%.6f RMSE=%.6f items=%d", result.FitBefore.LogLoss, result.FitBefore.RMSE, result.FitBefore.Items)
	t.Logf("FitAfter  (adapter new weights):  LogLoss=%.6f RMSE=%.6f items=%d", result.FitAfter.LogLoss, result.FitAfter.RMSE, result.FitAfter.Items)
	t.Logf("Improved()=%v", result.Improved())
}
