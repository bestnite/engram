package jobs

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"
	"gorm.io/gorm"

	"example.com/engram/internal/store"
)

// degradedWeights 把初始稳定性权重压到下限，造出一套与适配器输出明显不同的旧权重，
// 用来证明端到端里 before 读的是写回前的 preset，而不是把新权重复制到两边。
func degradedWeights() fsrs.Weights {
	w := fsrs.DefaultWeights()
	for i := 0; i < 4; i++ {
		w[i] = 0.001
	}
	return w
}

// setPresetWeightsForTest 直接把权重写进 preset 行，模拟「优化前该 preset 已有一套旧权重」。
func setPresetWeightsForTest(db *gorm.DB, presetID uint64, w fsrs.Weights) error {
	raw, err := json.Marshal(w[:])
	if err != nil {
		return err
	}
	return db.Model(&store.Preset{}).Where("id = ?", presetID).Update("weights_json", string(raw)).Error
}

// TestOptimizeEndToEndComputesFitMetrics 是 M9-11 验收 4：走真实适配器的端到端作业跑完后，
// job 行上的优化前后拟合指标都必须非零（此前恒为零，页面因此永远显示「未改善」）。
// preset 被预置一套与适配器输出不同的旧权重，因此 before/after 必须不同——这同时证明旧权重
// 是在 FinishOptimize 写回之前读取的，而不是把新权重复制到两边。本机已构建适配器，故真跑而非跳过。
func TestOptimizeEndToEndComputesFitMetrics(t *testing.T) {
	bin := optimizerBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("optimizer binary not built at %s; run `cargo build --release` in tools/optimizer", bin)
	}
	ctx := context.Background()
	db, st := newWiringDB(t)
	u, p := seedOptimizerUserAndPreset(t, db)
	const cards, days = 4, 10
	seedAdapterReviews(t, db, u.ID, cards, days)
	// 给 preset 一套与适配器输出不同的旧权重：既证明「旧权重在写回之前读取」，
	// 也让 after/before 的指标必须不同（不是同一次评估被复制到两边）。
	if err := setPresetWeightsForTest(db, p.ID, degradedWeights()); err != nil {
		t.Fatalf("seed stale preset weights: %v", err)
	}
	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, "2", nil, time.Now().UTC()); err != nil {
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
	if result.FitBefore == result.FitAfter {
		// 旧权重（写回前）与新权重落在同一指标上，说明两边其实评估了同一套权重。
		t.Fatalf("before/after metrics identical; the pre-optimization weights were not used: %+v", result.FitBefore)
	}
	t.Logf("Improved()=%v (direction is asserted in acceptance 1 with a fixed log)", result.Improved())
	// preset 行在作业结束后持适配器的新权重；旧权重已不复存在，证明 before 是写回前读到的。
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
	t.Logf("FitBefore (degraded old weights): LogLoss=%.6f RMSE=%.6f", result.FitBefore.LogLoss, result.FitBefore.RMSE)
	t.Logf("FitAfter  (adapter new weights):  LogLoss=%.6f RMSE=%.6f", result.FitAfter.LogLoss, result.FitAfter.RMSE)
	t.Logf("Improved()=%v", result.Improved())
}
