package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"example.com/engram/internal/store"
)

// 本文件是参数优化在本包的服务端门槛与结果写回（AGENTS.md §5 M9-5）。
// 权重拟合本身属于 M9-2 的 Rust 适配器；这里只负责「先判够不够，再建作业」与
// 「把适配器产出的拟合报告写进 job 行的 result_json」。

// ErrInsufficientReviews 是复习条数不足门槛的哨兵错误；调用方可用 errors.Is 判别，
// 并配合 ThresholdError 拿到具体差额用于页面提示（DESIGN.md §3.5：不足时显示「还差 N 条」）。
var ErrInsufficientReviews = errors.New("not enough reviews to optimize")

// ThresholdError 是门槛拒绝的详细结果：指名预设、已有条数、门槛与差额。
type ThresholdError struct {
	PresetID  uint64
	Reviews   int64
	Min       int
	Shortfall int64
}

// Error 输出英文失败原因，并把差额写进消息（AGENTS.md §2.1：日志与错误英文）。
func (e *ThresholdError) Error() string {
	return fmt.Sprintf("%v: preset %d has %d reviews, needs %d, short by %d",
		ErrInsufficientReviews, e.PresetID, e.Reviews, e.Min, e.Shortfall)
}

// Is 让 errors.Is(err, ErrInsufficientReviews) 成立，同时保留具体差额字段。
func (e *ThresholdError) Is(target error) bool { return target == ErrInsufficientReviews }

// EnqueueOptimize 是优化作业的唯一入队入口：先按门槛判定资格，再复用单并发 Enqueue。
// 条数不足时不建作业、不入队，直接返回 ThresholdError，让上层把差额展示给用户。
// ownerUserID 是预设所属用户，也就是复习日志的归属者（优化燃料按用户统计）。
func (r *Runner) EnqueueOptimize(ctx context.Context, ownerUserID, presetID uint64) (*store.Job, error) {
	gate, err := store.GateOptimize(ctx, r.db, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("check optimize threshold: %w", err)
	}
	if !gate.Eligible {
		return nil, &ThresholdError{
			PresetID:  presetID,
			Reviews:   gate.Reviews,
			Min:       gate.MinReviews,
			Shortfall: gate.Shortfall,
		}
	}
	target := presetID
	return r.Enqueue(ctx, KindOptimize, &target)
}

// FinishOptimize 把适配器产出的拟合报告写入 job 行的 result_json 并把作业置为 succeeded。
// 这是 M9-5「指标存在 job 行上」的落点；适配器缺席时（例如本轮的测试与占位）也可显式调用。
func (r *Runner) FinishOptimize(ctx context.Context, jobID uint64, result store.OptimizeResult, tail string) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode optimize result: %w", err)
	}
	return r.store.FinishSucceeded(ctx, jobID, r.now(), string(raw), tail)
}
