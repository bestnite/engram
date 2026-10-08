package schedule

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/store"
)

// allRecalledLog 造一份固定的复习日志：cards 张卡各 days 条复习（连续天数、全部 Good），
// 末尾再加一张只有一条复习的卡。全部回忆成功，因此「预测概率更高」的权重集必然拟合更好——
// 这让「更好的权重集」是已知的，供验收 1 断言 Improved() 的方向。
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
	// 只有一条复习的卡：没有先前的记忆状态，必须贡献 0 个可预测 item。
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

// fitTestPreset 造一个不用学习步骤、关闭 fuzz 的确定性 preset，权重由 jsonWeights 提供。
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

// betterWeights 返回一个「明显更好」的权重集：默认权重在「全部回忆成功」的日志上会
// 给出较高的预测概率，因此拟合理应优于下面的 worseWeights。
func betterWeights() []float64 {
	w := fsrs.DefaultWeights()
	return w[:]
}

// worseWeights 把初始稳定性权重压到下限，预测概率系统性偏低，在全部回忆成功的日志上
// 必然产生更大的对数损失——它是已知的「更差」权重集。
func worseWeights() fsrs.Weights {
	w := fsrs.DefaultWeights()
	for i := 0; i < 4; i++ {
		w[i] = 0.001
	}
	return w
}

// TestCompareFitKnownBetterWeights 是验收 1：固定复习日志、更好的权重集已知，
// 断言方向（更差权重在前、更好权重在后时 Improved() 为 true），并打印两组实际数值。
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

// TestCompareFitSameItemCount 是验收 2/3：两个指标覆盖相同的 item 数，
// 且每张卡的第一条复习被排除（只有一条复习的卡贡献 0 个 item）。
func TestCompareFitSameItemCount(t *testing.T) {
	const cards, days = 3, 6
	logs := allRecalledLog(cards, days)
	preset := fitTestPreset(t, jsonWeights(t, worseWeights()))

	before, after, err := CompareFit(preset, betterWeights(), logs)
	if err != nil {
		t.Fatalf("CompareFit: %v", err)
	}
	want := cards * (days - 1) // 每张卡丢掉首条复习；单条复习的卡贡献 0
	if before.Items != after.Items {
		t.Fatalf("item count mismatch: before=%d after=%d", before.Items, after.Items)
	}
	if before.Items != want {
		t.Fatalf("items = %d, want %d (first review of every card excluded)", before.Items, want)
	}
	t.Logf("items: before=%d after=%d want=%d", before.Items, after.Items, want)
}

// TestCompareFitNoPredictableItems 是验收 5（数据侧）：没有任何可预测 item 时
// 两个指标都是零值，Available() 为 false，页面因此不会声称「未改善」。
func TestCompareFitNoPredictableItems(t *testing.T) {
	preset := fitTestPreset(t, jsonWeights(t, worseWeights()))
	// 每张卡只有一条复习 → 全部被首条复习规则排除。
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
