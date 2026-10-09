package schedule

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"git.nite07.com/nite/engram/internal/store"
)

// 队列构建的文档化默认值。日切点默认值以 store 为唯一来源，
// 因为归一化规则（0/越界 → 默认）必须被 store/schedule/web/reminder/digest 共享。
const (
	DefaultDayCutoffHour = store.DefaultDayCutoffHour
	DefaultNewPerDay     = 20
	DefaultReviewsPerDay = 200
	DefaultReviewBatch   = 200
	DefaultQueueTimezone = "UTC"
)

// deckUnlimited 是「该卡组此项今日不限」的哨兵值。卡组列写 0 就是不限，不是「回落到默认」
// 因此「不限」需要与「剩余 0 张」区分开。
const deckUnlimited = -1

// ReviewOrder 决定到期复习卡的排序方式。
type ReviewOrder int

const (
	// OrderByRetrievability 按记忆保持概率升序（默认，最可能忘的先来）。
	OrderByRetrievability ReviewOrder = iota
	// OrderByDueAt 按到期时间升序（经典顺序）。
	OrderByDueAt
)

// NewOrder 决定新卡的出场顺序。
type NewOrder int

const (
	// NewOrderRandom 随机打乱，避免永远只背开头几张（默认）。
	NewOrderRandom NewOrder = iota
	// NewOrderCreated 按创建顺序，便于测试与\"按序引入\"的偏好。
	NewOrderCreated
)

// QueueKind 标识队列项的来源优先级。
type QueueKind int

const (
	// QueueLearning 是学习/再学习阶段且到期的卡，优先级最高。
	QueueLearning QueueKind = iota
	// QueueReview 是到期的复习卡。
	QueueReview
	// QueueNew 是新卡。
	QueueNew
)

// QueueOptions 是一次队列构建的入参。零值经 withDefaults 补齐为文档化默认值。
//
// 卡组范围（三种口径）：DeckIDs 非空（去重后）→ 取集合内所有卡组；
// DeckID 非 0 且 DeckIDs 为空 → 只取该卡组；两者都为空 → 该用户**可见**的卡组集合
// （store.DeckStore.VisibleIDs，与卡组列表页同一集合，排除别人的卡组）。
//
// 每日上限一律**按卡组算**（new_per_day/reviews_per_day 是卡组级列）：
// 范围里每个卡组各自用它的列与今日已用量算出剩余额度，多卡组/全库＝各卡组额度之和。
// 本包不再有任何「全局合计」路径。
//
// 覆盖优先级（仅用于测试与显式入口，生产入口不传）：
//  1. NewPerDayOverride / ReviewsPerDayOverride 非 nil —— 对范围内所有卡组统一生效，
//     0 表示不限（指针区分「未设置」与「显式 0」）；
//  2. NewPerDay / ReviewsPerDay 为正 —— 同样对范围内所有卡组统一生效（保留的旧入口）；
//  3. 否则读各卡组的 new_per_day / reviews_per_day，列写 0 表示不限；
//  4. 只有当卡组行不存在（读不到列）时才兜底 DefaultNewPerDay。
type QueueOptions struct {
	// DeckID 为 0 时跨该用户可见的全部卡组取卡；DeckIDs 非空时本字段被忽略。
	DeckID uint64
	// DeckIDs 是要复习的卡组集合；非空时（去重后）优先于 DeckID，空切片表示全库口径。
	//
	// 入参契约：**调用方必须先对每个 id 逐一判权**（每个 id 至少 reader 角色），任一 id 不可读
	// 即让整个请求失败。本构建器不校验权限、也不过无权限 id：这里的每个 id 都被当作可读卡组
	// 直接展开取卡。
	//
	// 为什么不把判权搬进 builder：判权下沉到 builder 后只剩「静默丢弃该卡组、返回其余卡片」
	// 这一种可表达的行为，会把「无权限卡组＝整次请求失败」退化成部分成功，与既定口径
	// 相悖（该口径由 REST 的 API.DueCards 与 web 的 loadDeckForRole 逐 id 兑现）。
	DeckIDs []uint64
	// Now 为观测时刻；零值表示使用当前时间。到期判定与复习日都以它为准。
	Now time.Time
	// Location 是用户时区；为 nil 时按 Timezone 名称加载（失败则退回 UTC）。
	Location *time.Location
	// Timezone 是 IANA 时区名，仅在 Location 为 nil 时使用。
	Timezone string
	// DayCutoffHour 是复习日切点（本地小时）；nil 默认 4，显式 0 是午夜。
	DayCutoffHour *int
	// NewPerDay 是每日新卡上限的调用方覆盖；<= 0 时改读卡组值。
	NewPerDay int
	// ReviewsPerDay 是每日复习上限的调用方覆盖；<= 0 时改读卡组值（卡组值为 0 表示不限）。
	ReviewsPerDay int
	// NewPerDayOverride / ReviewsPerDayOverride 是显式覆盖；非 nil 时优先于卡组设置。
	// 用指针是因为整型零值无法区分"未设置"与"显式 0（不限）"。
	NewPerDayOverride     *int
	ReviewsPerDayOverride *int
	// ReviewOrder 见 ReviewOrder。
	ReviewOrder ReviewOrder
	// NewOrder 见 NewOrder。
	NewOrder NewOrder
	// BatchSize 是复习卡一次取出的批大小（retrievability 需在内存排序）。
	BatchSize int
	// Rand 是可选随机源；仅影响 NewOrderRandom。为 nil 时按 (用户, 复习日) 派生固定种子，
	// 因此同一天内反复构建得到同一排列（见 queueShuffleSeed）。
	Rand *rand.Rand
}

// withDefaults 把零值字段补齐为文档化默认值。整型零值无法区分"未设置"与"显式 0"，
// 因此每日上限不在默认值里 —— 它们要先从各卡组读取（0 由卡组列表达"不限"）。
func (o QueueOptions) withDefaults() QueueOptions {
	if o.Timezone == "" {
		o.Timezone = DefaultQueueTimezone
	}

	if o.BatchSize == 0 {
		o.BatchSize = DefaultReviewBatch
	}
	return o
}

// QueueItem 是队列里的一张卡；Retrievability 只对复习卡有意义，其余为 0。
type QueueItem struct {
	CardID         uint64
	NoteID         uint64
	DeckID         uint64
	State          State
	DueAt          time.Time
	Retrievability float64
	Kind           QueueKind
}

// DeckQueueCounts 是单个卡组口径下队列的构成：New 是今日还能引入的新卡数，
// Review 是今日可刷的到期卡数（学习/再学习到期卡 + 受 reviews_per_day 限制的复习卡）。
// 两个数相加＝点进该卡组实际能刷的张数，列表页那两个数的语义就是它。
type DeckQueueCounts struct{ New, Review int }

// QueueBuilder 负责从 card_states / reviews 构造复习队列。
type QueueBuilder struct {
	db        *gorm.DB
	decks     *store.DeckStore
	scheduler *Scheduler
}

// NewQueueBuilder 构造队列构建器。
// decks 必填：全库口径要靠它解析「用户可见的卡组集合」。刻意不提供可选 setter ——
// 忘了注入曾经会静默退化成「不过滤卡组」，正是别人 private 卡组混进队列的原因。
func NewQueueBuilder(db *gorm.DB, decks *store.DeckStore, s *Scheduler) *QueueBuilder {
	return &QueueBuilder{db: db, decks: decks, scheduler: s}
}

// validate 校验构造队列所需的三项依赖，缺一即报英文错误（不静默降级）。
func (b *QueueBuilder) validate(userID uint64) error {
	if userID == 0 {
		return errors.New("schedule: build queue: user id is required")
	}
	if b.decks == nil {
		return errors.New("schedule: build queue: deck store is required")
	}
	if b.scheduler == nil {
		return errors.New("schedule: build queue: scheduler is required")
	}
	return nil
}

// stateRow 是查询中间结果的载体：cs 列 + 所属 card/note 的定位列。
type stateRow struct {
	CardID       uint64     `gorm:"column:card_id"`
	NoteID       uint64     `gorm:"column:note_id"`
	DeckID       uint64     `gorm:"column:deck_id"`
	State        string     `gorm:"column:state"`
	DueAt        *time.Time `gorm:"column:due_at"`
	Stability    *float64   `gorm:"column:stability"`
	Difficulty   *float64   `gorm:"column:difficulty"`
	LastReviewAt *time.Time `gorm:"column:last_review_at"`
}

// toCardState 把中间结果转成调度器可用的 CardState。
func (r stateRow) toCardState() *store.CardState {
	return &store.CardState{
		CardID:       r.CardID,
		State:        r.State,
		DueAt:        r.DueAt,
		Stability:    r.Stability,
		Difficulty:   r.Difficulty,
		LastReviewAt: r.LastReviewAt,
	}
}

// Build 按\"学习卡 → 到期复习卡 → 新卡\"的优先级构造队列。
// 学习卡不占额度；复习卡与新卡各自受所在卡组的 reviews_per_day / new_per_day 限制。
func (b *QueueBuilder) Build(ctx context.Context, userID uint64, opts QueueOptions) ([]QueueItem, error) {
	if err := b.validate(userID); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	now := opts.now()

	// 随机源在取卡之前确定：同一个源先决定「从每个卡组抽哪些新卡」（SQL 里的排序键），
	// 再决定合并后的出场顺序，两步都随 (用户, 复习日) 确定、可复现。
	var rng *rand.Rand
	var order *newCardOrder
	if opts.NewOrder == NewOrderRandom {
		rng = opts.Rand
		if rng == nil {
			loc, err := opts.location()
			if err != nil {
				return nil, err
			}
			rng = rand.New(rand.NewSource(queueShuffleSeed(userID, ReviewDay(now, loc, store.ResolveCutoff(opts.DayCutoffHour)))))
		}
		order = randomNewCardOrder(rng)
	}

	col, err := b.collect(ctx, userID, opts, order)
	if err != nil {
		return nil, err
	}

	// 复习卡：逐卡组取回后合并，按请求的方式全局排序，再截到 BatchSize（内存排序的上界）。
	reviews := col.mergeReviews()
	if opts.ReviewOrder == OrderByRetrievability {
		for i := range reviews {
			r, err := b.scheduler.Retrievability(reviews[i].row.toCardState(), now)
			if err != nil {
				return nil, fmt.Errorf("schedule: compute retrievability for card %d: %w", reviews[i].item.CardID, err)
			}
			reviews[i].item.Retrievability = r
		}
	}
	sort.SliceStable(reviews, func(i, j int) bool {
		if opts.ReviewOrder == OrderByRetrievability && reviews[i].item.Retrievability != reviews[j].item.Retrievability {
			return reviews[i].item.Retrievability < reviews[j].item.Retrievability
		}
		if !reviews[i].item.DueAt.Equal(reviews[j].item.DueAt) {
			return reviews[i].item.DueAt.Before(reviews[j].item.DueAt)
		}
		return reviews[i].item.CardID < reviews[j].item.CardID
	})
	if len(reviews) > opts.BatchSize {
		reviews = reviews[:opts.BatchSize]
	}

	fresh := col.mergeFresh()
	if rng != nil && len(fresh) > 1 {
		// 多卡组时各卡组的新卡按卡组顺序拼接，这里再打乱一次，让卡组之间也交错出场。
		rng.Shuffle(len(fresh), func(i, j int) { fresh[i], fresh[j] = fresh[j], fresh[i] })
	}

	out := make([]QueueItem, 0, len(col.learning)+len(reviews)+len(fresh))
	out = append(out, col.learning...)
	for i := range reviews {
		out = append(out, reviews[i].item)
	}
	out = append(out, fresh...)
	return out, nil
}

// queueShuffleSeed 从 (用户, 复习日) 派生新卡乱序的种子。
//
// 为什么不用「构建时刻」播种：那个值每次请求都不同，于是同一天里刷新页面、取下一批新卡都会
// 换一个排列——学习者看到的顺序不稳定，报告问题时也无从复现。改用 (用户, 复习日) 之后，
// 同一天内顺序固定（同一批次的顺序可复现），隔天自动轮换，仍满足「避免永远只背开头几张」
// 这一初衷；不同用户同一天也不共享排列。
//
// 复习日必须用本包唯一的 ReviewDay 定义（含用户时区与切点），否则轮换时点会与配额、连击、
// 提醒等一切按复习日计数的功能错开一天。
func queueShuffleSeed(userID uint64, day string) int64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], userID)
	h.Write(buf[:])
	h.Write([]byte(day))
	return int64(h.Sum64())
}

// DeckCounts 返回每个卡组单卡组口径下的队列构成；与 Build(DeckID=d) 出队的张数逐一相等
// （New = QueueNew，Review = 学习/再学习卡 + 到期复习卡）。
//
// 它与 Build 共用同一条取卡路径（collect）与同一条额度公式（deckBudget），因此列表页上的数字
// 就是点进去能刷的张数，不会再出现「列表显示有到期、点进去说没有」。deckIDs 为空时按全库口径
// （该用户可见卡组）解析，与 Build 的三种范围一致。ReviewOrder 固定为 OrderByDueAt：
// 数量统计不需要 retrievability，避免白算。
func (b *QueueBuilder) DeckCounts(ctx context.Context, userID uint64, deckIDs []uint64) (map[uint64]DeckQueueCounts, error) {
	if userID == 0 {
		return nil, errors.New("schedule: deck counts: user id is required")
	}
	if b.decks == nil {
		return nil, errors.New("schedule: deck counts: deck store is required")
	}
	opts := QueueOptions{DeckIDs: deckIDs, ReviewOrder: OrderByDueAt, NewOrder: NewOrderCreated}
	opts = opts.withDefaults()
	col, err := b.collect(ctx, userID, opts, nil)
	if err != nil {
		return nil, err
	}
	// 学习/再学习到期卡不占额度，但必须计入「复习」数：它们排在队列最前面，
	// 少算就会让列表上的两个数小于点进去能刷的张数（正是本次要修的现象之一）。
	learningByDeck := make(map[uint64]int, len(col.order))
	for _, it := range col.learning {
		learningByDeck[it.DeckID]++
	}
	out := make(map[uint64]DeckQueueCounts, len(col.order))
	for _, id := range col.order {
		review := len(col.reviews[id])
		if review > opts.BatchSize {
			review = opts.BatchSize
		}
		out[id] = DeckQueueCounts{New: len(col.fresh[id]), Review: learningByDeck[id] + review}
	}
	return out, nil
}

// deckScope 是一个卡组在本次构建中解析出的范围条目：它的今日剩余额度。
type deckScope struct {
	id uint64
	// newLeft / reviewLeft 是今日还能取多少张；deckUnlimited 表示不限。
	newLeft    int
	reviewLeft int
}

// dueReview 是取回的一张到期复习卡：队列项加上按需计算 retrievability 所需的状态行。
type dueReview struct {
	item QueueItem
	row  stateRow
}

// collectedQueue 是「取卡」阶段的中间结果，Build 与 DeckCounts 共用同一条路径。
type collectedQueue struct {
	// learning 是学习/再学习到期卡，全范围一条查询，due_at 升序（不受额度裁剪）。
	learning []QueueItem
	// reviews / fresh 按卡组分组，各自已按该卡组剩余额度裁剪。
	reviews map[uint64][]dueReview
	fresh   map[uint64][]QueueItem
	// order 是范围内的卡组顺序，保证合并结果确定。
	order []uint64
}

// mergeReviews 按范围顺序把各卡组的复习卡拼成一个切片；全局排序由调用方进行。
func (c *collectedQueue) mergeReviews() []dueReview {
	total := 0
	for _, id := range c.order {
		total += len(c.reviews[id])
	}
	out := make([]dueReview, 0, total)
	for _, id := range c.order {
		out = append(out, c.reviews[id]...)
	}
	return out
}

// mergeFresh 按范围顺序把各卡组的新卡拼成一个切片；打乱由调用方进行。
func (c *collectedQueue) mergeFresh() []QueueItem {
	total := 0
	for _, id := range c.order {
		total += len(c.fresh[id])
	}
	out := make([]QueueItem, 0, total)
	for _, id := range c.order {
		out = append(out, c.fresh[id]...)
	}
	return out
}

// collect 是 Build 与 DeckCounts 共用的取卡路径：解析范围与各卡组额度，再按卡组取回
// 学习卡（一条查询）、到期复习卡（逐卡组、限该卡组剩余额度）、新卡（逐卡组、限剩余额度）。
// order 为 nil 时新卡按创建顺序取，否则按 order 给出的伪随机排序键取。
func (b *QueueBuilder) collect(ctx context.Context, userID uint64, opts QueueOptions, order *newCardOrder) (*collectedQueue, error) {
	opts = opts.withDefaults()
	now := opts.now()

	scope, err := b.resolveScope(ctx, userID, opts)
	if err != nil {
		return nil, err
	}
	out := &collectedQueue{reviews: map[uint64][]dueReview{}, fresh: map[uint64][]QueueItem{}}
	if len(scope) == 0 {
		// 空集合＝没有可取的卡组（例如该用户没有任何可见卡组）：队列为空，不报错。
		return out, nil
	}

	deckIDs := make([]uint64, 0, len(scope))
	for _, d := range scope {
		deckIDs = append(deckIDs, d.id)
		out.order = append(out.order, d.id)
	}

	// 学习/再学习到期卡：一条查询取全部，不受额度裁剪（学习卡不占复习上限）。
	learning, err := b.learningDue(ctx, userID, now, deckIDs)
	if err != nil {
		return nil, err
	}
	out.learning = learning

	// 到期复习卡：逐卡组按各自剩余额度取；不限时每个卡组取 BatchSize，合并后由调用方截断。
	for _, d := range scope {
		if d.reviewLeft == 0 {
			continue
		}
		limit := d.reviewLeft
		if limit == deckUnlimited {
			limit = opts.BatchSize
		}
		rows, err := b.reviewDueDeck(ctx, userID, now, d.id, limit)
		if err != nil {
			return nil, err
		}
		out.reviews[d.id] = rows
	}

	// 新卡：逐卡组按各自剩余额度取；不限时不设 LIMIT。
	for _, d := range scope {
		if d.newLeft == 0 {
			continue
		}
		items, err := b.newCardsDeck(ctx, userID, d.id, d.newLeft, now, order)
		if err != nil {
			return nil, err
		}
		out.fresh[d.id] = items
	}
	return out, nil
}

// resolveScope 把三种卡组口径解析成「卡组 → 今日剩余额度」的列表。
// 范围：DeckIDs 非空（去重）→ 该集合；否则 DeckID != 0 → 单卡组；两者都空 → 该用户可见卡组。
// 随后加载各卡组的上限与今日已用量，逐卡组用 deckBudget 算剩余额度（规则只写一处）。
func (b *QueueBuilder) resolveScope(ctx context.Context, userID uint64, opts QueueOptions) ([]deckScope, error) {
	ids, err := b.scopeDeckIDs(ctx, userID, opts)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	caps, err := b.loadDeckCaps(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := opts.now()
	loc, err := opts.location()
	if err != nil {
		return nil, err
	}
	day := ReviewDay(now, loc, store.ResolveCutoff(opts.DayCutoffHour))
	usage, err := b.countUsage(ctx, userID, day, ids)
	if err != nil {
		return nil, err
	}

	out := make([]deckScope, 0, len(ids))
	for _, id := range ids {
		c, ok := caps[id]
		if !ok {
			// 读不到卡组列（卡组行不存在）才兜底默认；卡组列写 0 表示不限，不是回退默认。
			c = store.DeckCaps{NewPerDay: DefaultNewPerDay}
		}
		// 显式覆盖对范围内所有卡组统一生效；旧入口的正值字段同义（见 QueueOptions 文档）。
		if opts.NewPerDayOverride != nil {
			c.NewPerDay = *opts.NewPerDayOverride
		} else if opts.NewPerDay > 0 {
			c.NewPerDay = opts.NewPerDay
		}
		if opts.ReviewsPerDayOverride != nil {
			c.ReviewsPerDay = *opts.ReviewsPerDayOverride
		} else if opts.ReviewsPerDay > 0 {
			c.ReviewsPerDay = opts.ReviewsPerDay
		}
		newLeft, reviewLeft := deckBudget(c, usage[id].introduced, usage[id].reviewed)
		out = append(out, deckScope{id: id, newLeft: newLeft, reviewLeft: reviewLeft})
	}
	return out, nil
}

// scopeDeckIDs 把三种卡组口径归一成一个去重后的 id 集合：
// DeckIDs 非空时优先（去重），否则 DeckID 非 0 时返回单元素，两者都空时返回该用户可见卡组。
func (b *QueueBuilder) scopeDeckIDs(ctx context.Context, userID uint64, opts QueueOptions) ([]uint64, error) {
	if len(opts.DeckIDs) > 0 {
		return dedupeIDs(opts.DeckIDs), nil
	}
	if opts.DeckID != 0 {
		return []uint64{opts.DeckID}, nil
	}
	ids, err := b.decks.VisibleIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve visible decks: %w", err)
	}
	return ids, nil
}

// deckBudget 计算一个卡组今日剩余的新卡与复习额度；这是「每日额度如何算」的唯一实现，
// Build 与 DeckCounts 都经它，避免两处公式漂移。
// caps 里某项 <= 0 表示该项不限，返回 deckUnlimited；否则返回 max(cap-used, 0)。
func deckBudget(caps store.DeckCaps, introducedToday, reviewedToday int) (newLeft, reviewLeft int) {
	return budgetLeft(caps.NewPerDay, introducedToday), budgetLeft(caps.ReviewsPerDay, reviewedToday)
}

// budgetLeft 返回封顶后的剩余额度；cap <= 0 是不限。
func budgetLeft(cap, used int) int {
	if cap <= 0 {
		return deckUnlimited
	}
	if left := cap - used; left > 0 {
		return left
	}
	return 0
}

// dedupeIDs 去掉重复的卡组 id，保持首次出现的顺序（调用方传入的集合可能是无序的）。
func dedupeIDs(ids []uint64) []uint64 {
	out := make([]uint64, 0, len(ids))
	seen := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// location 解析用户时区；未指定时按 Timezone 加载，加载失败退回 UTC（不阻塞复习）。
func (o QueueOptions) location() (*time.Location, error) {
	if o.Location != nil {
		return o.Location, nil
	}
	loc, err := time.LoadLocation(o.Timezone)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// now 返回观测时刻（UTC）。
func (o QueueOptions) now() time.Time {
	if o.Now.IsZero() {
		return time.Now().UTC()
	}
	return o.Now.UTC()
}

// deckUsage 是一个卡组今日的用量：introduced 是今日引入的新卡数（state_before = 0），
// reviewed 是今日的复习量（state_before <> 0）。不建计数表，从 reviews 聚合。
type deckUsage struct{ introduced, reviewed int }

// loadDeckCaps 一次取回范围内各卡组的每日上限；卡组行不存在时该 id 不出现在结果里。
func (b *QueueBuilder) loadDeckCaps(ctx context.Context, deckIDs []uint64) (map[uint64]store.DeckCaps, error) {
	var rows []struct {
		ID            uint64 `gorm:"column:id"`
		NewPerDay     int    `gorm:"column:new_per_day"`
		ReviewsPerDay int    `gorm:"column:reviews_per_day"`
	}
	if err := b.db.WithContext(ctx).Table("decks").
		Select("id, new_per_day, reviews_per_day").
		Where("id IN ?", deckIDs).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("schedule: load deck caps: %w", err)
	}
	out := make(map[uint64]store.DeckCaps, len(rows))
	for _, r := range rows {
		out[r.ID] = store.DeckCaps{NewPerDay: r.NewPerDay, ReviewsPerDay: r.ReviewsPerDay}
	}
	return out, nil
}

// countUsage 按 (user_id, review_day) 从 reviews 聚合各卡组今日用量，一次分组查询取回。
// 新卡统计 state_before = 0，复习统计 state_before <> 0；额度是卡组级的，所以必须按卡组分组
// （new_per_day/reviews_per_day 定义在卡组上）。
func (b *QueueBuilder) countUsage(ctx context.Context, userID uint64, day string, deckIDs []uint64) (map[uint64]deckUsage, error) {
	var rows []struct {
		DeckID     uint64 `gorm:"column:deck_id"`
		Introduced int    `gorm:"column:introduced"`
		Reviewed   int    `gorm:"column:reviewed"`
	}
	if err := b.db.WithContext(ctx).Table("reviews AS r").
		Select("n.deck_id AS deck_id, "+
			"SUM(CASE WHEN r.state_before = ? THEN 1 ELSE 0 END) AS introduced, "+
			"SUM(CASE WHEN r.state_before <> ? THEN 1 ELSE 0 END) AS reviewed",
			int(StateNew), int(StateNew)).
		Joins("JOIN cards AS c ON c.id = r.card_id").
		Joins("JOIN notes AS n ON n.id = c.note_id").
		Where("r.user_id = ? AND r.review_day = ? AND n.deck_id IN ?", userID, day, deckIDs).
		Group("n.deck_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("schedule: count deck usage: %w", err)
	}
	out := make(map[uint64]deckUsage, len(rows))
	for _, r := range rows {
		out[r.DeckID] = deckUsage{introduced: r.Introduced, reviewed: r.Reviewed}
	}
	return out, nil
}

// baseStateQuery 是学习卡与复习卡共用的查询起点：只取未软删除且未暂停的卡，
// 并 join 到未软删除的 note 上（与 CardStore 的可见性规则一致）。
// deckIDs 是已解析的范围集合，总是非空（空集合在 collect 里提前返回）。
func (b *QueueBuilder) baseStateQuery(ctx context.Context, userID uint64, deckIDs []uint64) *gorm.DB {
	return b.db.WithContext(ctx).Table("card_states AS cs").
		Select("cs.card_id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id, "+
			"cs.state AS state, cs.due_at AS due_at, cs.stability AS stability, "+
			"cs.difficulty AS difficulty, cs.last_review_at AS last_review_at").
		Joins("JOIN cards AS cards ON cards.id = cs.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cs.user_id = ?", userID).
		Where("cards.suspended_at IS NULL").
		Where("notes.deck_id IN ?", deckIDs)
}

// learningDue 取学习/再学习阶段且已到期的卡，按到期时间升序。
func (b *QueueBuilder) learningDue(ctx context.Context, userID uint64, now time.Time, deckIDs []uint64) ([]QueueItem, error) {
	var rows []stateRow
	err := b.baseStateQuery(ctx, userID, deckIDs).
		Where("cs.state IN ?", []string{StateLearning.String(), StateRelearning.String()}).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now).
		Order("cs.due_at ASC, cs.card_id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load learning cards: %w", err)
	}
	return rowsToItems(rows, QueueLearning, now)
}

// reviewDueDeck 取单个卡组到期的复习卡，按 due_at 升序，最多 limit 张。
// 逐卡组取是为把各卡组自己的 reviews_per_day 落进 SQL 的 LIMIT；合并后的全局排序交给调用方。
func (b *QueueBuilder) reviewDueDeck(ctx context.Context, userID uint64, now time.Time, deckID uint64, limit int) ([]dueReview, error) {
	var rows []stateRow
	err := b.baseStateQuery(ctx, userID, []uint64{deckID}).
		Where("cs.state = ?", StateReview.String()).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now).
		Order("cs.due_at ASC, cs.card_id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load due reviews: %w", err)
	}
	out := make([]dueReview, 0, len(rows))
	for _, row := range rows {
		item, err := rowToItem(row, QueueReview, now)
		if err != nil {
			return nil, err
		}
		out = append(out, dueReview{item: item, row: row})
	}
	return out, nil
}

// newCardOrder 是新卡伪随机抽取的排序键参数（见 sortKeySQL）。
//
// 为什么在 SQL 里排序而不是取回后打乱：每日上限要落进 LIMIT，若先按创建顺序 LIMIT 再在内存里
// 打乱，被抽中的永远是最早创建的那几张，「随机」只改变了当天的出场顺序。参数随 (用户, 复习日)
// 的随机源变化，因此每个复习日抽到的集合不同。
type newCardOrder struct {
	Mul1 int64
	Add  int64
	Mul2 int64
}

const newCardOrderModulus = int64(1) << 31

// randomNewCardOrder 从随机源取一组排序键参数；两个乘数强制为奇数。
func randomNewCardOrder(rng *rand.Rand) *newCardOrder {
	return &newCardOrder{
		Mul1: rng.Int63n(newCardOrderModulus/2)*2 + 1,
		Add:  rng.Int63n(newCardOrderModulus),
		Mul2: rng.Int63n(newCardOrderModulus/2)*2 + 1,
	}
}

// sortKeySQL 返回按 cards.id 计算的排序键表达式，形如一个小型整数哈希：
//
//	h1  = ((id mod 2^31) × Mul1 + Add) mod 2^31
//	h2  = h1 xor (h1 / 2^16)          // xor 写成 (a|b) − (a&b)，两库都只有 | 与 &
//	key = (h2 × Mul2) mod 2^31
//
// 只用乘加取模时，连续的小 id 得到等差数列，排序几乎还是创建顺序；中间的异或打破线性。
// 三步在 [0, 2^31) 上都是双射（乘数为奇数），所以不会出现并列；每一步乘积都小于 2^62，
// 两库都不会溢出。表达式里只有本函数生成的整数，没有任何外部输入。
func (o *newCardOrder) sortKeySQL() string {
	m := newCardOrderModulus
	h1 := fmt.Sprintf("(((cards.id %% %d) * %d + %d) %% %d)", m, o.Mul1, o.Add, m)
	hi := fmt.Sprintf("(%s / 65536)", h1)
	h2 := fmt.Sprintf("((%s | %s) - (%s & %s))", h1, hi, h1, hi)
	return fmt.Sprintf("((%s * %d) %% %d)", h2, o.Mul2, m)
}

// newCardsDeck 取单个卡组的新卡：order 为 nil 时按创建顺序，否则按伪随机排序键。新卡的定义是
// \"状态为 new\"，包括尚无 card_states 行的卡（LEFT JOIN），这样刚加到共享卡组、用户还没产生
// 任何状态的行也能出现在队列里。已埋藏（due_at 被推到未来）的新卡不算本日新卡，因此额外要求
// due_at 未在未来。limit 为 deckUnlimited 时不设 LIMIT。
func (b *QueueBuilder) newCardsDeck(ctx context.Context, userID uint64, deckID uint64, limit int, now time.Time, order *newCardOrder) ([]QueueItem, error) {
	q := b.db.WithContext(ctx).Table("cards AS cards").
		Select("cards.id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id, "+
			"COALESCE(cs.state, 'new') AS state, cs.due_at AS due_at, cs.stability AS stability, "+
			"cs.difficulty AS difficulty, cs.last_review_at AS last_review_at").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Joins("LEFT JOIN card_states AS cs ON cs.card_id = cards.id AND cs.user_id = ?", userID).
		Where("cards.deleted_at IS NULL AND cards.suspended_at IS NULL").
		Where("(cs.card_id IS NULL OR cs.state = ?)", StateNew.String()).
		Where("(cs.due_at IS NULL OR cs.due_at <= ?)", now).
		Where("notes.deck_id = ?", deckID)
	if order != nil {
		// 必须包成 clause.OrderBy：GORM 的 Order 对裸 clause.Expr 静默忽略，不报错也不排序。
		q = q.Order(clause.OrderBy{Expression: clause.Expr{SQL: order.sortKeySQL() + ", cards.id ASC"}})
	} else {
		q = q.Order("cards.created_at ASC, cards.id ASC")
	}
	if limit != deckUnlimited {
		q = q.Limit(limit)
	}
	var rows []stateRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("schedule: load new cards: %w", err)
	}
	return rowsToItems(rows, QueueNew, now)
}

// rowToItem 把一行查询结果转成队列项；now 用于补齐新卡缺失的到期时间。
func rowToItem(row stateRow, kind QueueKind, now time.Time) (QueueItem, error) {
	state, err := ParseState(row.State)
	if err != nil {
		return QueueItem{}, err
	}
	due := derefTime(row.DueAt)
	if due.IsZero() {
		due = now
	}
	return QueueItem{
		CardID: row.CardID,
		NoteID: row.NoteID,
		DeckID: row.DeckID,
		State:  state,
		DueAt:  due.UTC(),
		Kind:   kind,
	}, nil
}

// rowsToItems 把查询结果转成队列项；now 用于补齐新卡缺失的到期时间。
func rowsToItems(rows []stateRow, kind QueueKind, now time.Time) ([]QueueItem, error) {
	items := make([]QueueItem, 0, len(rows))
	for _, row := range rows {
		it, err := rowToItem(row, kind, now)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// ReviewDay 按用户本地时间与切点计算复习日（YYYY-MM-DD）：本地时间减去 day_cutoff_hour 后取日期，
// 因此切点之前的凌晨时刻算作前一天。
//
// 已解析切点只对越界回退默认 4，午夜 0 保留，与 store.ReviewDayString 同口径：
// 复习日的写入（本包提交/埋藏）与读取（统计/连续天数/提醒/摘要）必须落在同一天，否则同一个
// 用户在两侧会看到不同的「今天」。可空用户配置先经 ResolveCutoff 解析，计算函数只收有效小时。
func ReviewDay(now time.Time, loc *time.Location, cutoffHour int) string {
	if loc == nil {
		loc = time.UTC
	}
	cutoffHour = store.NormalizedCutoff(cutoffHour)
	return now.In(loc).Add(-time.Duration(cutoffHour) * time.Hour).Format("2006-01-02")
}
