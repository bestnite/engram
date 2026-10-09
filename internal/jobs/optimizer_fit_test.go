//go:build unix

// 只在 Unix 上编译：依赖 jobs_test.go 里 unix-only 的测试脚手架（newTestRunner 等），
// 或直接以 `/bin/sh` 作为假命令。

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

// TestOptimizeEndToEndComputesFitMetrics 是验收 4：走真实适配器的端到端作业跑完后，
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
	// 8 张卡 × 40 天 = 320 条复习：既越过 300 条门槛下限，也让可预测 item 数
	// （320 - 每卡首条 = 312）稳稳超过 MinFitItems，端到端因此能给出真正的判定。
	const cards, days = 8, 40
	seedAdapterReviews(t, db, u.ID, p.ID, cards, days)
	// 给 preset 一套与适配器输出不同的旧权重：既证明「旧权重在写回之前读取」，
	// 也让 after/before 的指标必须不同（不是同一次评估被复制到两边）。
	if err := setPresetWeightsForTest(db, p.ID, degradedWeights()); err != nil {
		t.Fatalf("seed stale preset weights: %v", err)
	}
	// 门槛设到下限：存量低于下限会被钳到 300，而默认 500 会挡住这 320 条复习。
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
	// item 数必须随指标一起落进 result_json（fitMetrics 此前只搬 LogLoss/RMSE），
	// 且这批样本要大到足以判定「改善/未改善」。
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
	t.Logf("FitBefore (degraded old weights): LogLoss=%.6f RMSE=%.6f items=%d", result.FitBefore.LogLoss, result.FitBefore.RMSE, result.FitBefore.Items)
	t.Logf("FitAfter  (adapter new weights):  LogLoss=%.6f RMSE=%.6f items=%d", result.FitAfter.LogLoss, result.FitAfter.RMSE, result.FitAfter.Items)
	t.Logf("Improved()=%v", result.Improved())
}
