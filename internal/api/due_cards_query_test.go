package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"git.nite07.com/nite/engram/internal/schedule"
	"git.nite07.com/nite/engram/internal/store"
)

// countingLogger 统计 GORM 实际执行的 SQL 语句数：每调用一次 Trace 计一条。
// 它只计数、不改变执行，用于断言「查询数与队列长度无关」。
type countingLogger struct {
	mu sync.Mutex
	n  int
}

func (l *countingLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }
func (l *countingLogger) Info(context.Context, string, ...interface{})     {}
func (l *countingLogger) Warn(context.Context, string, ...interface{})     {}
func (l *countingLogger) Error(context.Context, string, ...interface{})    {}
func (l *countingLogger) Trace(context.Context, time.Time, func() (string, int64), error) {
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
}

func (l *countingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

func (l *countingLogger) reset() {
	l.mu.Lock()
	l.n = 0
	l.mu.Unlock()
}

// seedReviewCard 建一个 note + card + 一条 review 状态的 card_states（due_at 已过），
// 使该卡必然进入到期队列；version 用于断言乐观锁版本随队列返回。
func seedReviewCard(t *testing.T, db *gorm.DB, deckID, userID uint64, dueAt time.Time, front string, version int) store.Card {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{
		DeckID: deckID, Kind: "basic",
		FieldsJSON: fmt.Sprintf(`{"front":%q,"back":"a"}`, front),
		TagsJSON:   `["t1","t2"]`, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	due := dueAt.UTC()
	st := store.CardState{CardID: c.ID, UserID: userID, State: "review", DueAt: &due, Version: version}
	if err := db.Create(&st).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
	return c
}

// seedNewCard 建一个 note + card，不写 card_states：该卡是新卡。
func seedNewCard(t *testing.T, db *gorm.DB, deckID uint64, front string) store.Card {
	t.Helper()
	now := time.Now().UTC()
	n := store.Note{
		DeckID: deckID, Kind: "basic",
		FieldsJSON: fmt.Sprintf(`{"front":%q,"back":"a"}`, front),
		TagsJSON:   "[]", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: now}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c
}

// dueCardsStatements 装配一个含 n 张到期复习卡的卡组，调用 DueCards 并返回它发出的语句数。
func dueCardsStatements(t *testing.T, n int) (int, []DueCard) {
	t.Helper()
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, fmt.Sprintf("perf%d", n), store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	for i := 0; i < n; i++ {
		seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), fmt.Sprintf("q%d", i), 1)
	}
	logger := &countingLogger{}
	env.db.Logger = logger
	logger.reset()
	got, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, 500)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	return logger.count(), got
}

// TestDueCardsQueryCountIsConstant 断言 DueCards 装配阶段发出的语句数与队列长度无关：
// 「2 张卡的队列」与「50 张卡的队列」必须发出同样多的语句（常数级），而不是随卡数线性增长。
func TestDueCardsQueryCountIsConstant(t *testing.T) {
	small, gotSmall := dueCardsStatements(t, 2)
	large, gotLarge := dueCardsStatements(t, 50)
	if len(gotSmall) != 2 {
		t.Fatalf("2-card queue returned %d cards, want 2", len(gotSmall))
	}
	if len(gotLarge) != 50 {
		t.Fatalf("50-card queue returned %d cards, want 50", len(gotLarge))
	}
	t.Logf("DueCards statements: 2 cards = %d, 50 cards = %d", small, large)
	if small != large {
		t.Errorf("DueCards query count depends on queue length: 2 cards = %d statements, 50 cards = %d statements; want equal",
			small, large)
	}
}

// TestQueueBuildQueryCountIsConstant 记录队列构建（schedule.NewQueueBuilder.Build）的语句数：
// 它本来就按「集合/逐卡组」取数，语句数只随卡组数变化，不随卡数变化。
func TestQueueBuildQueryCountIsConstant(t *testing.T) {
	build := func(n int) int {
		t.Helper()
		env := newTestEnv(t, 600, 600)
		user := seedUser(t, env.db, fmt.Sprintf("build%d", n), store.RoleUser)
		deck := seedDeck(t, env.db, user.ID)
		for i := 0; i < n; i++ {
			seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), fmt.Sprintf("q%d", i), 1)
		}
		sched, err := env.api.schedulerForDeck(context.Background(), user.ID, deck)
		if err != nil {
			t.Fatalf("schedulerForDeck() error = %v", err)
		}
		builder := schedule.NewQueueBuilder(env.db, store.NewDeckStore(env.db), sched)
		logger := &countingLogger{}
		env.db.Logger = logger
		logger.reset()
		items, err := builder.Build(context.Background(), user.ID, schedule.QueueOptions{
			DeckID:        deck.ID,
			Now:           env.now,
			Timezone:      "UTC",
			DayCutoffHour: store.Ptr(4),
			ReviewOrder:   schedule.OrderByDueAt,
			NewOrder:      schedule.NewOrderCreated,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if len(items) != n {
			t.Fatalf("Build() returned %d items, want %d", len(items), n)
		}
		return logger.count()
	}
	small := build(2)
	large := build(50)
	t.Logf("QueueBuilder.Build statements: 2 cards = %d, 50 cards = %d", small, large)
	if small != large {
		t.Errorf("Build query count depends on card count: 2 cards = %d statements, 50 cards = %d statements; want equal",
			small, large)
	}
}

// TestDueCardsContractFields 守住 DueCards 的对外契约：卡组对外 id、卡顺序、Version、Template、
// DeckID、Kind/Fields/Tags 都必须由批量装配正确填充。
func TestDueCardsContractFields(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, "contract", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	first := seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-2*time.Hour), "q1", 7)
	second := seedReviewCard(t, env.db, deck.ID, user.ID, env.now.Add(-time.Hour), "q2", 9)

	got, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, 500)
	if err != nil {
		t.Fatalf("DueCards() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("DueCards() returned %d cards, want 2", len(got))
	}
	// 卡顺序：到期复习卡按 due_at 升序。
	if got[0].CardID != first.PublicID || got[1].CardID != second.PublicID {
		t.Fatalf("card order = [%s %s], want [%s %s]", got[0].CardID, got[1].CardID, first.PublicID, second.PublicID)
	}
	head := got[0]
	if head.CardID != first.PublicID {
		t.Errorf("CardID = %q, want %q", head.CardID, first.PublicID)
	}
	if head.NoteID == "" {
		t.Errorf("NoteID is empty")
	}
	if head.DeckID != deck.PublicID {
		t.Errorf("DeckID = %q, want deck public id %q", head.DeckID, deck.PublicID)
	}
	if head.State != "review" {
		t.Errorf("State = %q, want review", head.State)
	}
	if head.Version != 7 {
		t.Errorf("Version = %d, want 7", head.Version)
	}
	if head.Template != "forward" {
		t.Errorf("Template = %q, want forward", head.Template)
	}
	if head.Kind != "basic" {
		t.Errorf("Kind = %q, want basic", head.Kind)
	}
	if head.Fields["front"] != "q1" {
		t.Errorf("Fields[front] = %v, want q1", head.Fields["front"])
	}
	if len(head.Tags) != 2 || head.Tags[0] != "t1" || head.Tags[1] != "t2" {
		t.Errorf("Tags = %v, want [t1 t2]", head.Tags)
	}
	if !head.DueAt.Equal(env.now.Add(-2 * time.Hour)) {
		t.Errorf("DueAt = %v, want %v", head.DueAt, env.now.Add(-2*time.Hour))
	}
}

// TestDueCardsLimitClamp 守住 limit 的 [1,500] 夹取：下界 0→1，上界 >500→500。
// 上界用「不限新卡上限」的卡组 + 501 张新卡把队列撑到 500 以上来观察。
func TestDueCardsLimitClamp(t *testing.T) {
	env := newTestEnv(t, 600, 600)
	user := seedUser(t, env.db, "clamp", store.RoleUser)
	deck := seedDeck(t, env.db, user.ID)
	// 卡组每日上限设为 0＝不限，才能让 501 张新卡全部进入队列。
	if err := store.NewDeckStore(env.db).SetCaps(context.Background(), user.ID, deck.ID, store.DeckCaps{NewPerDay: 0, ReviewsPerDay: 0}); err != nil {
		t.Fatalf("SetCaps() error = %v", err)
	}
	const seeded = 501
	for i := 0; i < seeded; i++ {
		seedNewCard(t, env.db, deck.ID, fmt.Sprintf("n%d", i))
	}

	all, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, 0)
	if err != nil {
		t.Fatalf("DueCards(limit=0) error = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("DueCards(limit=0) returned %d cards, want 1 (clamped to lower bound)", len(all))
	}

	capped, err := env.api.DueCards(context.Background(), user, []uint64{deck.ID}, 9999)
	if err != nil {
		t.Fatalf("DueCards(limit=9999) error = %v", err)
	}
	if len(capped) != 500 {
		t.Errorf("DueCards(limit=9999) returned %d cards, want 500 (clamped to upper bound)", len(capped))
	}
}
