package schedule

import (
	"context"
	"errors"

	"git.nite07.com/nite/engram/internal/store"
)

// DeckBudget 是一个卡组今日的额度情况，供设置页显示「已用 / 剩余」。
//
// 两个 *Unlimited 字段表达「该项今日不限」：为 true 时对应的 PerDay / Left 数值无意义
// （写库的 0 表示不限，与「剩余 0 张」是同形的整数，必须靠布尔量区分，见 DESIGN.md §3.3）。
// 为 false 时 Left = max(PerDay-Used, 0)，Used 是今日已用量。
type DeckBudget struct {
	// NewPerDay / ReviewsPerDay 是卡组列上的每日上限；写 0 表示不限。
	NewPerDay     int
	ReviewsPerDay int
	// NewUsed / ReviewUsed 是今日已用（新卡＝今日引入数，复习＝今日复习数）。
	NewUsed    int
	ReviewUsed int
	// NewLeft / ReviewLeft 是今日剩余额度；仅当对应的 Unlimited 为 false 时有意义。
	NewLeft    int
	ReviewLeft int
	// NewUnlimited / ReviewUnlimited 为 true 表示该项今日不限（PerDay 为 0）。
	NewUnlimited    bool
	ReviewUnlimited bool
}

// DeckBudgets 返回每个卡组今日的额度情况。
//
// 它与 Build / DeckCounts 共用同一条额度公式（deckBudget）与同一个复习日口径
// （QueueOptions 的默认 Location / DayCutoffHour，与 DeckCounts 一致），因此设置页上的
// 「已用 / 剩余」与队列实际能出的张数是同源的，网页层不重写 SQL 也不重算公式。
//
// deckIDs 是待查询的卡组集合；空集合返回空 map（不报错）。读不到卡组列（卡组行不存在）
// 时沿用 resolveScope 的兜底：NewPerDay 用 DefaultNewPerDay，ReviewsPerDay 保持 0（不限）。
func (b *QueueBuilder) DeckBudgets(ctx context.Context, userID uint64, deckIDs []uint64) (map[uint64]DeckBudget, error) {
	if userID == 0 {
		return nil, errors.New("schedule: deck budgets: user id is required")
	}
	if b.decks == nil {
		return nil, errors.New("schedule: deck budgets: deck store is required")
	}
	ids := dedupeIDs(deckIDs)
	out := make(map[uint64]DeckBudget, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	// 口味与 DeckCounts 完全相同：只用默认的时区与切点，数量/额度统计与 retrievability 无关。
	opts := QueueOptions{DeckIDs: ids}
	opts = opts.withDefaults()

	caps, err := b.loadDeckCaps(ctx, ids)
	if err != nil {
		return nil, err
	}
	loc, err := opts.location()
	if err != nil {
		return nil, err
	}
	day := ReviewDay(opts.now(), loc, store.ResolveCutoff(opts.DayCutoffHour))
	usage, err := b.countUsage(ctx, userID, day, ids)
	if err != nil {
		return nil, err
	}

	for _, id := range ids {
		c, ok := caps[id]
		if !ok {
			// 读不到卡组列（卡组行不存在）才兜底默认；与 resolveScope 的规则保持一处。
			c = store.DeckCaps{NewPerDay: DefaultNewPerDay}
		}
		u := usage[id]
		newLeft, reviewLeft := deckBudget(c, u.introduced, u.reviewed)
		out[id] = DeckBudget{
			NewPerDay:       c.NewPerDay,
			ReviewsPerDay:   c.ReviewsPerDay,
			NewUsed:         u.introduced,
			ReviewUsed:      u.reviewed,
			NewLeft:         nonNegative(newLeft),
			ReviewLeft:      nonNegative(reviewLeft),
			NewUnlimited:    newLeft == deckUnlimited,
			ReviewUnlimited: reviewLeft == deckUnlimited,
		}
	}
	return out, nil
}

// nonNegative 把 deckUnlimited 哨兵折成 0，避免把 -1 泄漏给调用方；是否不限由布尔字段表达。
func nonNegative(left int) int {
	if left < 0 {
		return 0
	}
	return left
}
