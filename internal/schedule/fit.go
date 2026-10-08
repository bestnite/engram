package schedule

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件实现：在 Go 里用 go-fsrs 重放复习日志，算出「优化前/后」的拟合指标。
//
// 动机：要求作业行携带优化前后的拟合对比，预设页据此渲染「改善/未改善」，但此前
// 没有任何代码计算它，FitBefore/FitAfter 恒为零，于是 Improved() 永远为 false，页面在
// 权重真的变好时也告诉用户「未改善」。这里把两个指标算出来：
//
//   - 用待评估的权重集重放每张卡的复习序列（fsrs.NewFSRS）；
//   - 在每条复习「之前」用 (*FSRS).Retrievability 取预测记住概率；
//   - 与实际结果比对（评分 > 1 即算记住）；
//   - LogLoss = 平均负对数似然，RMSE = 均方根误差，两者覆盖同一批 item；
//   - 每张卡的第一条复习被跳过（它没有先前的记忆状态，预测概率为 0，不可评估）；
//   - 同一批 item 分别在两套权重下评估：preset 当前的旧权重与适配器产出的新权重。

// FitMetrics 是一次「用某套权重在固定复习集上回放」得到的拟合指标。
// Items 是本次实际评估的 item 数（已排除首条复习与不可预测点），用于证明两套权重覆盖同一批 item。
type FitMetrics struct {
	// LogLoss 是平均负对数似然；越小越贴合实际结果。
	LogLoss float64
	// RMSE 是预测概率与实际结果（0/1）的均方根误差；越小越贴合。
	RMSE float64
	// Items 是参与计算的 item 数（两张卡、两次权重的指标口径必须一致）。
	Items int
}

// CompareFit 用 preset 的当前权重（旧）与 newWeights（新）在同一批可预测 item 上各评估一次，
// 返回 (before, after)。两套权重共用同一份 preset 参数装配（目标保留率、学习步骤、最大间隔、
// fuzz），仅权重不同，因此回放口径与应用真实排程一致。
//
// 同一批 item 的保证：先按卡片分组构建一次 item 列表，再分别回放两套权重；只有当某个 item
// 在两套权重下都能预测（Retrievability 不报错且概率 > 0）时才计入，否则一并跳过。
func CompareFit(preset *store.Preset, newWeights []float64, logs []store.OptimizerReviewLog) (before, after FitMetrics, err error) {
	if len(newWeights) != len(fsrs.DefaultWeights()) {
		return FitMetrics{}, FitMetrics{}, fmt.Errorf("schedule: new weights have %d values, want %d", len(newWeights), len(fsrs.DefaultWeights()))
	}
	beforeParam, err := Parameters(preset)
	if err != nil {
		return FitMetrics{}, FitMetrics{}, err
	}
	afterParam, err := ParametersWithWeights(preset, fsrs.Weights(newWeights))
	if err != nil {
		return FitMetrics{}, FitMetrics{}, err
	}

	groups := groupReviews(logs)
	items := buildFitItems(groups)
	beforePreds := replayPredictions(fsrs.NewFSRS(beforeParam), groups)
	afterPreds := replayPredictions(fsrs.NewFSRS(afterParam), groups)
	before, after = scoreItems(items, beforePreds, afterPreds)
	return before, after, nil
}

// fitKey 唯一定位一个「可预测 item」：某张卡（分组下标）复习序列里的第 seq 次复习。
type fitKey struct {
	card int
	seq  int
}

// fitItem 是一个可评估的预测点：某张卡在 seq 次（seq>=1）复习「之前」的预测，及其真实结果。
type fitItem struct {
	key      fitKey
	observed float64 // 1 表示记住（评分 > 1），0 表示忘记（评分 Again）
}

// groupReviews 把复习日志按卡片分组，组内按复习时刻升序。
// 输入日志已按 (reviewed_at, id) 升序（ReviewStore.ExportOptimizerLog 的契约），
// 这里仍显式稳定排序，避免调用方换数据源后回放口径悄悄变化。
func groupReviews(logs []store.OptimizerReviewLog) [][]store.OptimizerReviewLog {
	index := make(map[uint64]int)
	var groups [][]store.OptimizerReviewLog
	for _, lg := range logs {
		gi, ok := index[lg.CardID]
		if !ok {
			gi = len(groups)
			index[lg.CardID] = gi
			groups = append(groups, nil)
		}
		groups[gi] = append(groups[gi], lg)
	}
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return g[i].ReviewTime < g[j].ReviewTime })
	}
	return groups
}

// buildFitItems 构建规范 item 列表：跳过每张卡的第一条复习（无先前记忆状态），
// 跳过评分非法的行（无法判定结果，也无法推进状态）。
func buildFitItems(groups [][]store.OptimizerReviewLog) []fitItem {
	var items []fitItem
	for gi, reviews := range groups {
		for i, lg := range reviews {
			if i == 0 {
				continue
			}
			if _, ok := ratingFromInt(lg.ReviewRating); !ok {
				continue
			}
			items = append(items, fitItem{
				key:      fitKey{card: gi, seq: i},
				observed: observedOutcome(lg.ReviewRating),
			})
		}
	}
	return items
}

// replayPredictions 用给定 FSRS 实例重放所有卡片，返回每个可预测 item 的预测记住概率。
// 预测取在该条复习「之前」的状态上计算；某条复习无法预测（New 状态、stability<=0、
// 评分非法）时该 item 缺席，并且该卡后续也无法继续可靠回放，故就此打住。
func replayPredictions(f *fsrs.FSRS, groups [][]store.OptimizerReviewLog) map[fitKey]float64 {
	preds := make(map[fitKey]float64)
	for gi, reviews := range groups {
		if len(reviews) < 2 {
			continue
		}
		card := fsrs.NewCard(time.UnixMilli(reviews[0].ReviewTime).UTC())
		for i, lg := range reviews {
			rating, ok := ratingFromInt(lg.ReviewRating)
			if !ok {
				break
			}
			at := time.UnixMilli(lg.ReviewTime).UTC()
			if i > 0 {
				p, err := f.Retrievability(card, at)
				if err == nil && p > 0 {
					preds[fitKey{card: gi, seq: i}] = p
				}
			}
			next, err := f.Next(card, at, rating)
			if err != nil {
				break
			}
			card = next.Card
		}
	}
	return preds
}

// scoreItems 在同一批 item 上分别汇总两套权重的指标。一个 item 只有在两套权重下都有预测时
// 才计入，从而保证 before/after 覆盖完全相同的 item 集合、Items 数一致。
func scoreItems(items []fitItem, beforePreds, afterPreds map[fitKey]float64) (FitMetrics, FitMetrics) {
	var b, a fitAccumulator
	for _, it := range items {
		pb, okBefore := beforePreds[it.key]
		pa, okAfter := afterPreds[it.key]
		if !okBefore || !okAfter {
			continue
		}
		b.add(pb, it.observed)
		a.add(pa, it.observed)
	}
	return b.metrics(), a.metrics()
}

// fitAccumulator 累积单个权重集的负对数似然与平方误差，最后归一化成两个指标。
type fitAccumulator struct {
	items int
	loss  float64
	sqErr float64
}

// add 记入一个 item 的预测概率与真实结果（0/1）。
func (a *fitAccumulator) add(p, y float64) {
	p = clampProbability(p)
	a.items++
	if y > 0 {
		a.loss += -math.Log(p)
	} else {
		a.loss += -math.Log(1 - p)
	}
	d := p - y
	a.sqErr += d * d
}

// metrics 返回均值化的指标；没有 item 时返回零值（等价于「无可用指标」）。
func (a *fitAccumulator) metrics() FitMetrics {
	if a.items == 0 {
		return FitMetrics{}
	}
	n := float64(a.items)
	return FitMetrics{
		LogLoss: a.loss / n,
		RMSE:    math.Sqrt(a.sqErr / n),
		Items:   a.items,
	}
}

// clampProbability 把预测概率夹到开区间 (0,1)，避免 ln(0) 产生 ±Inf。
func clampProbability(p float64) float64 {
	const eps = 1e-15
	if p < eps {
		return eps
	}
	if p > 1-eps {
		return 1 - eps
	}
	return p
}

// observedOutcome 把评分映射成结果：评分 > 1（Hard/Good/Easy）算记住，Again 算忘记。
func observedOutcome(rating int) float64 {
	if rating > 1 {
		return 1
	}
	return 0
}

// ratingFromInt 校验复习日志里的整数评分；非法值无法推进回放。
func ratingFromInt(rating int) (fsrs.Rating, bool) {
	if rating < int(fsrs.Again) || rating > int(fsrs.Easy) {
		return 0, false
	}
	return fsrs.Rating(rating), true
}
