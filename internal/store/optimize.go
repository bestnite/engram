package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 本文件是参数优化（ROADMAP.md M9-5）在 store 层的部分：
// 服务端门槛（复习条数不足直接拒绝并给出差额）与拟合报告的数据形态。
//
// 分工边界：权重拟合算法属于 M9-2 的 Rust 适配器，永不进 Go 代码；这里只定义
// 「门槛怎么判定」与「指标写在哪、叫什么」，让 web 层（M9-4）与适配器有稳定契约。

const (
	// DefaultOptimizeMinReviews 是优化门槛的默认值（默认 < 500 条拒绝）。
	// 管理员可通过 settings 表的 SettingKeyOptimizeMinReviews 调整。
	DefaultOptimizeMinReviews = 500

	// SettingKeyOptimizeMinReviews 是门槛覆盖值的设置键。值按 settings 表约定以 JSON
	// 编码文本存储（见 PutSetting / LoadSettings），内容为十进制整数条数。
	SettingKeyOptimizeMinReviews = "optimize.min_reviews"

	// MinOptimizeMinReviews 是优化门槛的下限（ROADMAP.md M9-12）。
	// 依据：250 卡 / 1969 条真日志的实测里，可用 item 少于约 184 时适配器学不动或落回
	// 默认权重，优化前后指标相同，页面会对一次本就没机会的优化报「未改善」。184 item
	// 约合 210 条复习，取 300 条留出余量。低于下限的门槛拦不住任何会产生误导结论的优化。
	MinOptimizeMinReviews = 300
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
	if n < MinOptimizeMinReviews {
		// 读取路径也钳到下限：settings 行可能来自直接写库、旧版本或绕过表单的写入，
		// 不能假设表单校验一定跑过。注意与上面的默认回退语义不同——这里是「值合法但太低」，
		// 钳到下限而不是退回默认值。
		return MinOptimizeMinReviews, nil
	}
	return n, nil
}

// CountByUser 返回某用户的复习日志总条数，是优化门槛的输入（唯一燃料）。
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

// FitMetrics 是优化前后各测一次的拟合指标（设置页的「优化前后拟合对比」，
// 对应 Anki 手册的 "Check health" 思路：用历史复习反推参数对实际结果的贴合度）。
//
// 指标定义（由 internal/schedule 用 go-fsrs 回放复习日志算出，见 ROADMAP.md M9-11）：
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
	// Items 是本次评估实际覆盖的可预测 item 数（已排除每张卡的首条复习与不可预测点）。
	// 加它是为了把「算出来了」与「算出来了但样本太小」区分开（ROADMAP.md M9-12）：
	// 旧 result_json 没有这个字段，反序列化得到 0，向后兼容。
	Items int `json:"items"`
}

// MinFitItems 是可信判定「改善/未改善」所需的最小 item 数（ROADMAP.md M9-12）。
// 依据：可用 item 少于约 184 时适配器学不动或落回默认权重，优化前后指标相同，
// 「未改善」是对一次本就没机会的优化的误导；取 200 留出余量，实测 184 item 起判定稳定。
const MinFitItems = 200

// OptimizeResult 是 optimize 作业 result_json 的结构（jobs.result_json）。
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

// Available 报告这次拟合是否真的覆盖了至少一个可预测 item。
// 没有任何可预测 item（例如复习日志全是每张卡的首条复习、或全部无法回放）时，
// CompareFit 返回零值；预测概率被夹在开区间 (0,1) 内，LogLoss 恒为正，因此零值即「无指标」。
// 预设页据此避免在无指标时给出「未改善」这种误导性结论（ROADMAP.md M9-11 验收 5）。
func (m FitMetrics) Available() bool { return m.LogLoss > 0 }

// SampleSufficient 报告这次拟合的样本是否足够支撑「改善/未改善」结论。
// 与 Available() 语义不同：Available() 只说明「指标算出来了」（至少覆盖一个 item），
// 本方法进一步要求覆盖的 item 数达到 MinFitItems。两者都为真时页面才渲染结论；
// 只有 Available() 为真时页面渲染「样本不足，无法判定」。
func (m FitMetrics) SampleSufficient() bool { return m.Available() && m.Items >= MinFitItems }

// ErrPresetWeightsIDRequired 表示回退默认权重时未给出预设主键。
var ErrPresetWeightsIDRequired = errors.New("reset preset weights: id is required")

// ResetPresetWeights 把预设的优化权重一键回退为默认权重（M9 验收「可一键回退」）：weights_json、
// weights_optimized_at、weights_review_count 三列一并写回 NULL。
//
// 为什么三列必须一起清：调度器（schedule.NewScheduler）以 weights_json 是否为 NULL 决定用
// DefaultWeights() 还是优化权重；只把 weights_json 清掉、却留下 weights_optimized_at 与
// weights_review_count，会让页面显示「已优化 + 时间 + 条数」却没有实际权重，自相矛盾。
// 一次性清三列后，读到的预设与从未优化过的新预设完全一致。
//
// 幂等：本来就是默认（三列为 NULL）的预设再调用一次也返回 nil——「已经是默认」不是错误，
// UI 的按钮可能被重复点击。只有 owner 能回退，授权规则与 Update 一致（preset 不参与 M5-1 的授权表）。
func (s *PresetStore) ResetPresetWeights(ctx context.Context, actorUserID, presetID uint64) error {
	if presetID == 0 {
		return ErrPresetWeightsIDRequired
	}
	existing, err := s.ByID(ctx, presetID)
	if err != nil {
		return err
	}
	if err := requirePresetOwner(existing, actorUserID); err != nil {
		return err
	}
	// 用 map 显式写入 nil：GORM 对 map 更新不会跳过零值，因此这里确实是把列置为 NULL。
	updates := map[string]any{
		"weights_json":         nil,
		"weights_optimized_at": nil,
		"weights_review_count": nil,
		"updated_at":           time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Model(&Preset{}).Where("id = ?", presetID).Updates(updates).Error; err != nil {
		return fmt.Errorf("reset preset weights: %w", err)
	}
	return nil
}
