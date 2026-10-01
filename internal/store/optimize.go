package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 本文件是参数优化（DESIGN.md §3.5、AGENTS.md §5 M9-5）在 store 层的部分：
// 服务端门槛（复习条数不足直接拒绝并给出差额）与拟合报告的数据形态。
//
// 分工边界：权重拟合算法属于 M9-2 的 Rust 适配器，永不进 Go 代码；这里只定义
// 「门槛怎么判定」与「指标写在哪、叫什么」，让 web 层（M9-4）与适配器有稳定契约。

const (
	// DefaultOptimizeMinReviews 是优化门槛的默认值（DESIGN.md §3.5：默认 < 500 条拒绝）。
	// 管理员可通过 settings 表的 SettingKeyOptimizeMinReviews 调整。
	DefaultOptimizeMinReviews = 500

	// SettingKeyOptimizeMinReviews 是门槛覆盖值的设置键。值按 settings 表约定以 JSON
	// 编码文本存储（见 PutSetting / LoadSettings），内容为十进制整数条数。
	SettingKeyOptimizeMinReviews = "optimize.min_reviews"
)

// OptimizeMinReviews 读取当前的优化门槛。未设置、空值或非法值时退回默认值：
// 管理员手误（例如写成 "many"）绝不能让优化入口永久不可用，退回默认是可解释的行为。
func OptimizeMinReviews(ctx context.Context, db *gorm.DB) (int, error) {
	settings, err := LoadSettings(ctx, db)
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(settings[SettingKeyOptimizeMinReviews])
	if raw == "" {
		return DefaultOptimizeMinReviews, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		// 非正整数门槛没有意义（>=1 才可能拦住任何东西）；退回默认并让调用方照常放行。
		return DefaultOptimizeMinReviews, nil
	}
	return n, nil
}

// CountByUser 返回某用户的复习日志总条数，是优化门槛的输入（唯一燃料，DESIGN.md §3.5）。
func (s *ReviewStore) CountByUser(ctx context.Context, userID uint64) (int64, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&Review{}).
		Where("user_id = ?", userID).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

// OptimizeGate 是某个用户相对当前门槛的资格判定结果。
// Shortfall 只在 Eligible 为 false 时有意义，等于「还差多少条」。
type OptimizeGate struct {
	Reviews    int64
	MinReviews int
	Shortfall  int64
	Eligible   bool
}

// GateOptimize 判定某用户是否有资格运行优化：读门槛、数复习、算差额。
func GateOptimize(ctx context.Context, db *gorm.DB, userID uint64) (OptimizeGate, error) {
	if userID == 0 {
		return OptimizeGate{}, gorm.ErrPrimaryKeyRequired
	}
	min, err := OptimizeMinReviews(ctx, db)
	if err != nil {
		return OptimizeGate{}, err
	}
	reviews, err := NewReviewStore(db).CountByUser(ctx, userID)
	if err != nil {
		return OptimizeGate{}, err
	}
	gate := OptimizeGate{Reviews: reviews, MinReviews: min}
	if reviews < int64(min) {
		gate.Shortfall = int64(min) - reviews
	} else {
		gate.Eligible = true
	}
	return gate, nil
}

// FitMetrics 是优化前后各测一次的拟合指标（DESIGN.md §3.5「优化前后拟合对比」，
// 对应 Anki 手册的 "Check health" 思路：用历史复习反推参数对实际结果的贴合度）。
//
// 指标定义（由 M9-2 的适配器计算，Go 侧只承载与比较）：
//   - LogLoss：每次到期复习的预测对数损失，predicted 是参数对「该次复习会回忆起来」
//     给出的概率（0-1），observed 取 1（非 Again）或 0（Again）。越小越贴合；
//     这是与 Anki 优化器输出 magnitude 同量纲、可跨参数集直接比较的标量。
//   - RMSE：预测可回忆概率与实际结果（0/1）的均方根误差，是 LogLoss 的直观辅助，
//     对极端置信（预测 0/1 却判错）比 LogLoss 更温和，便于页面展示。
//
// 两者都在固定复习集上计算，before 用优化前的旧权重、after 用新权重；只比较同一
// 数据集上的两个标量，避免把「换了数据集」误当成拟合改善。
type FitMetrics struct {
	LogLoss float64 `json:"log_loss"`
	RMSE    float64 `json:"rmse"`
}

// OptimizeResult 是 optimize 作业 result_json 的结构（jobs.result_json，DESIGN.md §2.2）。
// 落在 job 行而非 preset 行：报告属于「这一次优化」，preset 只保存最终生效的权重
// （weights_json / weights_optimized_at / weights_review_count）。
type OptimizeResult struct {
	// ReviewsUsed 是本次训练实际使用的复习条数，便于解释为什么阈值刚好通过。
	ReviewsUsed int64 `json:"reviews_used"`
	// Weights 是适配器产出的 21 维 FSRS 权重，尚未（或已经）写回 preset。
	Weights []float64 `json:"weights"`
	// FitBefore / FitAfter 是同一复习集上用旧/新权重算出的拟合指标。
	FitBefore FitMetrics `json:"fit_before"`
	FitAfter  FitMetrics `json:"fit_after"`
	// OptimizedAt 是适配器完成训练的时刻（UTC）。
	OptimizedAt time.Time `json:"optimized_at"`
}

// Improved 判断新权重的对数损失是否优于旧权重；相等视为未改善。
func (r OptimizeResult) Improved() bool { return r.FitAfter.LogLoss < r.FitBefore.LogLoss }
