//go:build unix

// 只在 Unix 上编译：假命令走 `/bin/sh -c ...`（理由同 jobs_test.go 的文件头说明）。

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedOptimizeReviews 直接写入 n 条复习日志，作为优化门槛的燃料。
// 只关心条数，字段取最小合法值；不建 cards/notes，因为 reviews 没有外键约束。
func seedOptimizeReviews(t *testing.T, db *gorm.DB, userID uint64, n int) {
	t.Helper()
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		rv := store.Review{
			CardID:      1,
			UserID:      userID,
			Rating:      3,
			GradeSource: "self",
			ReviewedAt:  at.Add(time.Duration(i) * time.Minute),
			ReviewDay:   "2026-10-02",
			StateBefore: 2,
		}
		if err := db.Create(&rv).Error; err != nil {
			t.Fatalf("create review %d: %v", i, err)
		}
	}
}

// okBuilder 返回一条必然成功退出的命令，供需要真实执行作业的用例使用。
func okBuilder(context.Context, *store.Job, Reporter) (Command, error) {
	return Command{Name: "/bin/sh", Args: []string{"-c", "exit 0"}}, nil
}

// TestOptimiseThresholdRefusesBelowThreshold 是「阈值」用例（验收之一）：复习太少的预设被拒绝，
// 且错误里指名差额（还差多少条）。默认门槛 500，这里只有 3 条，差额应为 497。
func TestOptimiseThresholdRefusesBelowThreshold(t *testing.T) {
	runner, _, db := newTestRunner(t, time.Second, okBuilder)
	seedOptimizeReviews(t, db, 1, 3)

	_, err := runner.EnqueueOptimize(context.Background(), 1, 42)
	if !errors.Is(err, ErrInsufficientReviews) {
		t.Fatalf("EnqueueOptimize() error = %v, want ErrInsufficientReviews", err)
	}
	var te *ThresholdError
	if !errors.As(err, &te) {
		t.Fatalf("EnqueueOptimize() error %v does not carry *ThresholdError", err)
	}
	wantShortfall := int64(store.DefaultOptimizeMinReviews - 3)
	if te.Shortfall != wantShortfall {
		t.Errorf("Shortfall = %d, want %d", te.Shortfall, wantShortfall)
	}
	if te.PresetID != 42 || te.Reviews != 3 || te.Min != store.DefaultOptimizeMinReviews {
		t.Errorf("ThresholdError = %+v, want preset 42 / reviews 3 / min %d", te, store.DefaultOptimizeMinReviews)
	}
	if !strings.Contains(err.Error(), "short by 497") {
		t.Errorf("error %q does not name the shortfall (want %q)", err.Error(), "short by 497")
	}
	t.Logf("below-threshold refusal: %v", err)
}

// TestOptimiseThresholdReadsSettings 证明门槛可由管理员通过 settings 覆盖，且覆盖值受
// 300 条下限约束：存量写成 2（绕过表单直接写库）会被读取路径钳到 300，
// 于是刚好 300 条复习可以入队——若读取端仍按 2 放行，这个用例会因门槛形同虚设而失去意义。
func TestOptimiseThresholdReadsSettings(t *testing.T) {
	runner, _, db := newTestRunner(t, time.Second, okBuilder)
	ctx := context.Background()
	seedOptimizeReviews(t, db, 1, store.MinOptimizeMinReviews)

	if err := store.PutSetting(ctx, db, store.SettingKeyOptimizeMinReviews, "2", nil, time.Now().UTC()); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	min, err := store.OptimizeMinReviews(ctx, db)
	if err != nil {
		t.Fatalf("OptimizeMinReviews: %v", err)
	}
	if min != store.MinOptimizeMinReviews {
		t.Fatalf("OptimizeMinReviews() with stored 2 = %d, want the floor %d", min, store.MinOptimizeMinReviews)
	}

	job, err := runner.EnqueueOptimize(ctx, 1, 42)
	if err != nil {
		t.Fatalf("EnqueueOptimize() with the floor threshold: %v", err)
	}
	if job.TargetID == nil || *job.TargetID != 42 {
		t.Errorf("job.TargetID = %v, want 42", job.TargetID)
	}
}

// TestOptimiseResultStoresFitMetricsOnJobRow 是另一条验收：拟合指标存在 job 行上。
// 适配器缺席时走 FinishOptimize 占位路径，断言 result_json 里 before/after 可解析。
func TestOptimiseResultStoresFitMetricsOnJobRow(t *testing.T) {
	runner, st, _ := newTestRunner(t, time.Second, okBuilder)
	ctx := context.Background()

	job, err := st.CreateQueued(ctx, KindOptimize, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateQueued: %v", err)
	}
	result := store.OptimizeResult{
		ReviewsUsed: 500,
		Weights:     []float64{0.4, 0.6, 2.4},
		FitBefore:   store.FitMetrics{LogLoss: 0.5412, RMSE: 0.6011},
		FitAfter:    store.FitMetrics{LogLoss: 0.5103, RMSE: 0.5877},
		OptimizedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
	if err := runner.FinishOptimize(ctx, job.ID, result, "adapter ok"); err != nil {
		t.Fatalf("FinishOptimize: %v", err)
	}

	got, err := st.ByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %q, want %q", got.Status, StatusSucceeded)
	}
	if got.ResultJSON == nil {
		t.Fatal("ResultJSON is nil, want the fit report to be stored on the job row")
	}
	var decoded store.OptimizeResult
	if err := json.Unmarshal([]byte(*got.ResultJSON), &decoded); err != nil {
		t.Fatalf("decode result_json %q: %v", *got.ResultJSON, err)
	}
	if decoded.FitBefore.LogLoss != result.FitBefore.LogLoss || decoded.FitAfter.LogLoss != result.FitAfter.LogLoss {
		t.Errorf("log loss before/after = %v/%v, want %v/%v",
			decoded.FitBefore.LogLoss, decoded.FitAfter.LogLoss, result.FitBefore.LogLoss, result.FitAfter.LogLoss)
	}
	if decoded.FitBefore.RMSE != result.FitBefore.RMSE || decoded.FitAfter.RMSE != result.FitAfter.RMSE {
		t.Errorf("rmse before/after = %v/%v, want %v/%v",
			decoded.FitBefore.RMSE, decoded.FitAfter.RMSE, result.FitBefore.RMSE, result.FitAfter.RMSE)
	}
	if decoded.ReviewsUsed != 500 || len(decoded.Weights) != 3 {
		t.Errorf("result_json = %+v, want reviews_used 500 and 3 weights", decoded)
	}
	if !decoded.Improved() {
		t.Errorf("Improved() = false, want true (after log loss %v < before %v)",
			decoded.FitAfter.LogLoss, decoded.FitBefore.LogLoss)
	}
	t.Logf("job %d result_json = %s", got.ID, *got.ResultJSON)
}
