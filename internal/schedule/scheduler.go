// Package schedule 是 go-fsrs 的薄封装：把数据库里的整数状态/评分与 FSRS v6 的类型互转，
// 并在此之上做队列构建。服务只做三件事 —— 持久化状态、构造队列、把评分喂给调度器
// （DESIGN.md §3.1），因此本包刻意不引入业务规则之外的抽象。
//
// 整数约定（DESIGN.md §3.4、AGENTS.md §2.3 第 3 条）：评分用 1–4（Again/Hard/Good/Easy），
// 状态用 0–3（New/Learning/Review/Relearning），与 FSRS 生态的复习日志格式一致，
// 将来接优化器零转换。数据库里 state 存字符串（new/learning/review/relearning），
// 在边界处转成整数。
package schedule

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/open-spaced-repetition/go-fsrs/v4"

	"example.com/flashcard/internal/store"
)

// Rating 是四档自评分；取值 1–4，与 reviews.rating 一致。
type Rating int

// 四档评分。数值是契约的一部分，不得调整（数据库列与优化器导出依赖它们）。
const (
	Again Rating = 1
	Hard  Rating = 2
	Good  Rating = 3
	Easy  Rating = 4
)

// String 返回英文名，仅用于日志与错误信息（DESIGN.md §10.4：给机器看的用英文）。
func (r Rating) String() string {
	switch r {
	case Again:
		return "again"
	case Hard:
		return "hard"
	case Good:
		return "good"
	case Easy:
		return "easy"
	default:
		return "unknown"
	}
}

// Valid 判断评分是否落在 1–4 内。
func (r Rating) Valid() bool { return r >= Again && r <= Easy }

// State 是卡片所处的调度阶段；取值 0–3，与 reviews.state_before 一致。
type State int

// 四个调度阶段；数值与 FSRS 的 State 以及评审日志约定一致。
const (
	StateNew        State = 0
	StateLearning   State = 1
	StateReview     State = 2
	StateRelearning State = 3
)

// ParseState 把数据库里的字符串状态转成整数。未知取值报英文错误而不是静默归一化：
// 静默会掩盖迁移或调用方拼写错误。
func ParseState(s string) (State, error) {
	switch s {
	case "new":
		return StateNew, nil
	case "learning":
		return StateLearning, nil
	case "review":
		return StateReview, nil
	case "relearning":
		return StateRelearning, nil
	default:
		return 0, fmt.Errorf("schedule: unknown card state %q", s)
	}
}

// String 返回数据库里使用的字符串状态，是本包与 store 之间的唯一翻译点。
func (s State) String() string {
	switch s {
	case StateNew:
		return "new"
	case StateLearning:
		return "learning"
	case StateReview:
		return "review"
	case StateRelearning:
		return "relearning"
	default:
		return "unknown"
	}
}

// fsrsState 把整数状态转成 go-fsrs 的 State。
func (s State) fsrsState() fsrs.State { return fsrs.State(s) }

// stateFromFSRS 把 go-fsrs 的 State 转回整数状态。
func stateFromFSRS(s fsrs.State) State { return State(s) }

// Outcome 是一次评分（或预览）的调度结果；字段与 card_states / reviews 的写入口径对齐，
// 让 M3-3 的提交事务可以直接取用而不必再算第二遍。
type Outcome struct {
	Rating         Rating
	Due            time.Time // UTC 到期时间
	State          State
	Stability      float64
	Difficulty     float64
	ScheduledDays  int
	Reps           int
	Lapses         int
	RemainingSteps int
	// IntervalDays 是本次评分产生的实际间隔（天，可为小数，学习步骤以分钟计）。
	IntervalDays float64
}

// Scheduler 持有由某个 preset 构造出的 FSRS 参数；无状态，可并发复用。
type Scheduler struct {
	fsrs *fsrs.FSRS
}

// NewScheduler 从 preset 构造调度器：权重取 preset.WeightsJSON（支持 17/19/21 维自动迁移），
// 为 NULL 时用 fsrs.DefaultWeights()；学习/再学习步骤、目标保留率、最大间隔与 fuzz 均来自 preset
// （DESIGN.md §3.2、§3.5）。
func NewScheduler(preset *store.Preset) (*Scheduler, error) {
	if preset == nil {
		return nil, fmt.Errorf("schedule: preset is required")
	}
	weights, err := presetWeights(preset.WeightsJSON)
	if err != nil {
		return nil, err
	}
	learning, err := parseSteps(preset.LearningSteps)
	if err != nil {
		return nil, fmt.Errorf("schedule: parse learning steps: %w", err)
	}
	relearning, err := parseSteps(preset.RelearningSteps)
	if err != nil {
		return nil, fmt.Errorf("schedule: parse relearning steps: %w", err)
	}

	p := fsrs.DefaultParam()
	p.RequestRetention = preset.DesiredRetention
	p.MaximumInterval = float64(preset.MaximumIntervalDays)
	p.EnableFuzz = preset.FuzzEnabled()
	p.W = weights
	p.LearningSteps = learning
	p.RelearningSteps = relearning
	// DefaultParam 保持 EnableShortTerm=true，与 DESIGN.md §3.2 的\"学习步骤 + 短期记忆\"一致。

	return &Scheduler{fsrs: fsrs.NewFSRS(p)}, nil
}

// presetWeights 解析 preset.WeightsJSON。NULL / 空 / "null" 均视为使用默认权重。
func presetWeights(raw *string) (fsrs.Weights, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return fsrs.DefaultWeights(), nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "null" {
		return fsrs.DefaultWeights(), nil
	}
	var arr []float64
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return fsrs.Weights{}, fmt.Errorf("schedule: decode preset weights: %w", err)
	}
	w, err := fsrs.MigrateWeights(arr)
	if err != nil {
		return fsrs.Weights{}, fmt.Errorf("schedule: migrate preset weights: %w", err)
	}
	return w, nil
}

// parseSteps 解析 "1m,10m" 这类学习步骤；空串表示关闭学习步骤（返回 nil）。
// 支持 s/m/h/d 后缀，缺省按分钟处理，与 preset 的文档化默认值一致。
func parseSteps(spec string) ([]float64, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ",")
	steps := make([]float64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		value, err := stepMinutes(part)
		if err != nil {
			return nil, err
		}
		steps = append(steps, value)
	}
	if len(steps) == 0 {
		return nil, nil
	}
	return steps, nil
}

// stepMinutes 把单个步骤（如 "10m"）转成分钟数。
func stepMinutes(part string) (float64, error) {
	idx := len(part)
	for idx > 0 {
		c := part[idx-1]
		if (c >= '0' && c <= '9') || c == '.' {
			break
		}
		idx--
	}
	num, unit := part[:idx], strings.ToLower(part[idx:])
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid step %q", part)
	}
	switch unit {
	case "", "m", "min":
		return v, nil
	case "s", "sec":
		return v / 60, nil
	case "h", "hr":
		return v * 60, nil
	case "d":
		return v * 1440, nil
	default:
		return 0, fmt.Errorf("invalid step unit in %q", part)
	}
}

// cardFromState 把数据库行转成 go-fsrs 的 Card。
//
// 学习步骤游标：go-fsrs 的 RemainingSteps 是「还剩几步」的倒计时，card_states.step_index 的语义
// 与之同向（DESIGN.md §3.3 已冻结为「剩余」步数）——不要读成「已走步数」，否则状态机会整体反着跑。
// 首次评分之后由 FSRS 接管该字段。
func cardFromState(st *store.CardState) (fsrs.Card, error) {
	state, err := ParseState(st.State)
	if err != nil {
		return fsrs.Card{}, err
	}
	c := fsrs.Card{
		Due:            derefTime(st.DueAt),
		Stability:      derefFloat(st.Stability),
		Difficulty:     derefFloat(st.Difficulty),
		ScheduledDays:  uint64(maxInt(st.ScheduledDays, 0)),
		Reps:           uint64(maxInt(st.Reps, 0)),
		Lapses:         uint64(maxInt(st.Lapses, 0)),
		State:          state.fsrsState(),
		LastReview:     derefTime(st.LastReviewAt),
		RemainingSteps: st.StepIndex,
	}
	return c, nil
}

// Preview 预览四档评分的调度结果，不改动任何状态；返回顺序固定为 Again/Hard/Good/Easy。
func (s *Scheduler) Preview(st *store.CardState, now time.Time) ([]Outcome, error) {
	card, err := cardFromState(st)
	if err != nil {
		return nil, err
	}
	log, err := s.fsrs.Repeat(card, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("schedule: preview card %d: %w", st.CardID, err)
	}
	order := []Rating{Again, Hard, Good, Easy}
	out := make([]Outcome, 0, len(order))
	for _, rating := range order {
		out = append(out, outcomeFrom(rating, log[fsrs.Rating(rating)], now))
	}
	return out, nil
}

// Next 提交一个评分并返回推进后的状态。
func (s *Scheduler) Next(st *store.CardState, now time.Time, rating Rating) (Outcome, error) {
	if !rating.Valid() {
		return Outcome{}, fmt.Errorf("schedule: invalid rating %d", int(rating))
	}
	card, err := cardFromState(st)
	if err != nil {
		return Outcome{}, err
	}
	info, err := s.fsrs.Next(card, now.UTC(), fsrs.Rating(rating))
	if err != nil {
		return Outcome{}, fmt.Errorf("schedule: next card %d: %w", st.CardID, err)
	}
	return outcomeFrom(rating, info, now), nil
}

// Retrievability 返回卡片在 now 时点的记忆保持概率，供队列排序使用；学习/新卡返回 0。
func (s *Scheduler) Retrievability(st *store.CardState, now time.Time) (float64, error) {
	card, err := cardFromState(st)
	if err != nil {
		return 0, err
	}
	return s.fsrs.Retrievability(card, now.UTC())
}

// outcomeFrom 把 go-fsrs 的结果摊平成整数导向的 Outcome。
func outcomeFrom(rating Rating, info fsrs.SchedulingInfo, now time.Time) Outcome {
	due := info.Card.Due.UTC()
	interval := due.Sub(now.UTC()).Hours() / 24
	if interval < 0 {
		interval = 0
	}
	return Outcome{
		Rating:         rating,
		Due:            due,
		State:          stateFromFSRS(info.Card.State),
		Stability:      info.Card.Stability,
		Difficulty:     info.Card.Difficulty,
		ScheduledDays:  int(info.Card.ScheduledDays),
		Reps:           int(info.Card.Reps),
		Lapses:         int(info.Card.Lapses),
		RemainingSteps: info.Card.RemainingSteps,
		IntervalDays:   interval,
	}
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func derefFloat(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
