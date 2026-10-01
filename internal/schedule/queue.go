package schedule

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"time"

	"gorm.io/gorm"

	"example.com/flashcard/internal/store"
)

// 队列构建的文档化默认值（DESIGN.md §3.3）。
const (
	DefaultDayCutoffHour = 4
	DefaultNewPerDay     = 20
	DefaultReviewsPerDay = 200
	DefaultReviewBatch   = 200
	DefaultQueueTimezone = "UTC"
)

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
	// NewOrderRandom 随机打乱，避免永远只背开头几张（DESIGN.md §3.3 默认）。
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
type QueueOptions struct {
	// DeckID 为 0 时跨该用户的全部卡组取卡。
	DeckID uint64
	// Now 为观测时刻；零值表示使用当前时间。到期判定与复习日都以它为准。
	Now time.Time
	// Location 是用户时区；为 nil 时按 Timezone 名称加载（失败则退回 UTC）。
	Location *time.Location
	// Timezone 是 IANA 时区名，仅在 Location 为 nil 时使用。
	Timezone string
	// DayCutoffHour 是复习日切点（本地小时），默认 4。
	DayCutoffHour int
	// NewPerDay 是每日新卡上限。
	NewPerDay int
	// ReviewsPerDay 是每日复习上限；0 表示不限。
	ReviewsPerDay int
	// ReviewOrder 见 ReviewOrder。
	ReviewOrder ReviewOrder
	// NewOrder 见 NewOrder。
	NewOrder NewOrder
	// BatchSize 是复习卡一次取出的批大小（retrievability 需在内存排序）。
	BatchSize int
	// Rand 是可选随机源；仅影响 NewOrderRandom。为 nil 时按当前时间播种。
	Rand *rand.Rand
}

// DefaultQueueOptions 返回 DESIGN.md §3.3 的文档化默认值（新卡 20、复习 200、
// 复习卡按 retrievability 升序、新卡随机）。零值 QueueOptions 不等价于本返回值：
// 零值的 ReviewsPerDay 表示\"不限\"，因此显式默认值必须由调用方在此取得后再覆盖。
func DefaultQueueOptions() QueueOptions {
	return QueueOptions{
		Timezone:      DefaultQueueTimezone,
		DayCutoffHour: DefaultDayCutoffHour,
		NewPerDay:     DefaultNewPerDay,
		ReviewsPerDay: DefaultReviewsPerDay,
		ReviewOrder:   OrderByRetrievability,
		NewOrder:      NewOrderRandom,
		BatchSize:     DefaultReviewBatch,
	}
}

// withDefaults 把零值字段补齐为文档化默认值。整型零值无法区分\"未设置\"与\"显式 0\"，
// 因此 ReviewsPerDay 只有 0（不限）一种含义，其余字段 0 均视为未设置。
func (o QueueOptions) withDefaults() QueueOptions {
	if o.Timezone == "" {
		o.Timezone = DefaultQueueTimezone
	}
	if o.DayCutoffHour == 0 {
		o.DayCutoffHour = DefaultDayCutoffHour
	}
	if o.NewPerDay == 0 {
		o.NewPerDay = DefaultNewPerDay
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

// QueueBuilder 负责从 card_states / reviews 构造复习队列。
type QueueBuilder struct {
	db        *gorm.DB
	scheduler *Scheduler
}

// NewQueueBuilder 构造队列构建器；scheduler 用于计算复习卡的 retrievability。
func NewQueueBuilder(db *gorm.DB, s *Scheduler) *QueueBuilder {
	return &QueueBuilder{db: db, scheduler: s}
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

// Build 按\"学习卡 → 到期复习卡 → 新卡\"的优先级构造队列（DESIGN.md §3.3）。
// 学习卡不占复习上限；复习卡受 ReviewsPerDay 限制；新卡受 NewPerDay 与\"今日已引入\"限制。
func (b *QueueBuilder) Build(ctx context.Context, userID uint64, opts QueueOptions) ([]QueueItem, error) {
	if userID == 0 {
		return nil, errors.New("schedule: build queue: user id is required")
	}
	if b.scheduler == nil {
		return nil, errors.New("schedule: build queue: scheduler is required")
	}
	opts = opts.withDefaults()
	loc, err := opts.location()
	if err != nil {
		return nil, err
	}
	now := opts.now()
	day := ReviewDay(now, loc, opts.DayCutoffHour)

	learning, err := b.learningDue(ctx, userID, now, opts.DeckID)
	if err != nil {
		return nil, err
	}
	reviews, err := b.reviewDue(ctx, userID, now, opts.DeckID, opts)
	if err != nil {
		return nil, err
	}
	if opts.ReviewsPerDay > 0 {
		used, err := b.countReviews(ctx, userID, day, false)
		if err != nil {
			return nil, err
		}
		remaining := opts.ReviewsPerDay - used
		if remaining < 0 {
			remaining = 0
		}
		if len(reviews) > remaining {
			reviews = reviews[:remaining]
		}
	}

	introduced, err := b.countReviews(ctx, userID, day, true)
	if err != nil {
		return nil, err
	}
	newCap := opts.NewPerDay - introduced
	if newCap < 0 {
		newCap = 0
	}
	fresh, err := b.newCards(ctx, userID, opts.DeckID, newCap)
	if err != nil {
		return nil, err
	}
	if opts.NewOrder == NewOrderRandom && len(fresh) > 1 {
		rng := opts.Rand
		if rng == nil {
			rng = rand.New(rand.NewSource(now.UnixNano()))
		}
		rng.Shuffle(len(fresh), func(i, j int) { fresh[i], fresh[j] = fresh[j], fresh[i] })
	}

	out := make([]QueueItem, 0, len(learning)+len(reviews)+len(fresh))
	out = append(out, learning...)
	out = append(out, reviews...)
	out = append(out, fresh...)
	return out, nil
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

// baseStateQuery 是学习卡与复习卡共用的查询起点：只取未软删除且未暂停的卡，
// 并 join 到未软删除的 note 上（与 CardStore 的可见性规则一致）。
func (b *QueueBuilder) baseStateQuery(ctx context.Context, userID, deckID uint64) *gorm.DB {
	q := b.db.WithContext(ctx).Table("card_states AS cs").
		Select("cs.card_id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id, "+
			"cs.state AS state, cs.due_at AS due_at, cs.stability AS stability, "+
			"cs.difficulty AS difficulty, cs.last_review_at AS last_review_at").
		Joins("JOIN cards AS cards ON cards.id = cs.card_id AND cards.deleted_at IS NULL").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Where("cs.user_id = ?", userID).
		Where("cards.suspended_at IS NULL")
	if deckID != 0 {
		q = q.Where("notes.deck_id = ?", deckID)
	}
	return q
}

// learningDue 取学习/再学习阶段且已到期的卡，按到期时间升序。
func (b *QueueBuilder) learningDue(ctx context.Context, userID uint64, now time.Time, deckID uint64) ([]QueueItem, error) {
	var rows []stateRow
	err := b.baseStateQuery(ctx, userID, deckID).
		Where("cs.state IN ?", []string{StateLearning.String(), StateRelearning.String()}).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now).
		Order("cs.due_at ASC, cs.card_id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load learning cards: %w", err)
	}
	return rowsToItems(rows, QueueLearning, now)
}

// reviewDue 取到期的复习卡。先按 due_at 升序取一批（BatchSize），
// 再按请求的排序方式整理：retrievability 需要在内存算，故不能只靠 SQL。
func (b *QueueBuilder) reviewDue(ctx context.Context, userID uint64, now time.Time, deckID uint64, opts QueueOptions) ([]QueueItem, error) {
	var rows []stateRow
	err := b.baseStateQuery(ctx, userID, deckID).
		Where("cs.state = ?", StateReview.String()).
		Where("cs.due_at IS NOT NULL AND cs.due_at <= ?", now).
		Order("cs.due_at ASC, cs.card_id ASC").
		Limit(opts.BatchSize).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("schedule: load due reviews: %w", err)
	}
	items, err := rowsToItems(rows, QueueReview, now)
	if err != nil {
		return nil, err
	}
	if opts.ReviewOrder == OrderByRetrievability {
		for i := range items {
			r, err := b.scheduler.Retrievability(rows[i].toCardState(), now)
			if err != nil {
				return nil, fmt.Errorf("schedule: compute retrievability for card %d: %w", items[i].CardID, err)
			}
			items[i].Retrievability = r
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Retrievability != items[j].Retrievability {
				return items[i].Retrievability < items[j].Retrievability
			}
			return items[i].DueAt.Before(items[j].DueAt)
		})
	}
	return items, nil
}

// newCards 取新卡。新卡的定义是\"状态为 new\"，包括尚无 card_states 行的卡（LEFT JOIN），
// 这样刚加到共享卡组、用户还没产生任何状态的行也能出现在队列里。
func (b *QueueBuilder) newCards(ctx context.Context, userID, deckID uint64, limit int) ([]QueueItem, error) {
	if limit <= 0 {
		return nil, nil
	}
	q := b.db.WithContext(ctx).Table("cards AS cards").
		Select("cards.id AS card_id, cards.note_id AS note_id, notes.deck_id AS deck_id, "+
			"COALESCE(cs.state, 'new') AS state, cs.due_at AS due_at, cs.stability AS stability, "+
			"cs.difficulty AS difficulty, cs.last_review_at AS last_review_at").
		Joins("JOIN notes AS notes ON notes.id = cards.note_id AND notes.deleted_at IS NULL").
		Joins("LEFT JOIN card_states AS cs ON cs.card_id = cards.id AND cs.user_id = ?", userID).
		Where("cards.deleted_at IS NULL AND cards.suspended_at IS NULL").
		Where("(cs.card_id IS NULL OR cs.state = ?)", StateNew.String()).
		Order("cards.created_at ASC, cards.id ASC").
		Limit(limit)
	if deckID != 0 {
		q = q.Where("notes.deck_id = ?", deckID)
	}
	var rows []stateRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("schedule: load new cards: %w", err)
	}
	return rowsToItems(rows, QueueNew, time.Now().UTC())
}

// rowsToItems 把查询结果转成队列项；now 用于补齐新卡缺失的到期时间。
func rowsToItems(rows []stateRow, kind QueueKind, now time.Time) ([]QueueItem, error) {
	items := make([]QueueItem, 0, len(rows))
	for _, row := range rows {
		state, err := ParseState(row.State)
		if err != nil {
			return nil, err
		}
		due := derefTime(row.DueAt)
		if due.IsZero() {
			due = now
		}
		items = append(items, QueueItem{
			CardID: row.CardID,
			NoteID: row.NoteID,
			DeckID: row.DeckID,
			State:  state,
			DueAt:  due.UTC(),
			Kind:   kind,
		})
	}
	return items, nil
}

// countReviews 按 (user_id, review_day) 从 reviews 聚合计数（DESIGN.md §2.2：不建计数表）。
// newOnly=true 统计\"今日引入的新卡\"（state_before = 0），false 统计\"今日的复习量\"（state_before > 0）。
func (b *QueueBuilder) countReviews(ctx context.Context, userID uint64, day string, newOnly bool) (int, error) {
	q := b.db.WithContext(ctx).Model(&store.Review{}).
		Where("user_id = ? AND review_day = ?", userID, day)
	if newOnly {
		q = q.Where("state_before = ?", int(StateNew))
	} else {
		q = q.Where("state_before <> ?", int(StateNew))
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return 0, fmt.Errorf("schedule: count reviews: %w", err)
	}
	return int(n), nil
}

// ReviewDay 按用户本地时间与切点计算复习日（YYYY-MM-DD）：本地时间减去 day_cutoff_hour 后取日期，
// 因此切点之前的凌晨时刻算作前一天（DESIGN.md §3.3）。
func ReviewDay(now time.Time, loc *time.Location, cutoffHour int) string {
	if loc == nil {
		loc = time.UTC
	}
	return now.In(loc).Add(-time.Duration(cutoffHour) * time.Hour).Format("2006-01-02")
}
