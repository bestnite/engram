package schedule

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// newTestDB 建一个临时 SQLite 库并跑全部模型迁移（AGENTS.md §2.5：不 mock 数据库）。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "schedule.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(store.AllModels()...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return db
}

// seedDeck 建一个预设 + 私有卡组，返回卡组 id。
func seedDeck(t *testing.T, db *gorm.DB, now time.Time) uint64 {
	t.Helper()
	p := store.NewPreset(1, "preset")
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: 1, Name: "Deck", Description: "", Visibility: store.DeckVisibilityPrivate, PresetID: p.ID, CreatedAt: now}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}

// seedCard 建一个 basic note 与它的一张 card，返回 card id。
func seedCard(t *testing.T, db *gorm.DB, deckID uint64, template string, createdAt time.Time) uint64 {
	t.Helper()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`, TagsJSON: "[]", CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: template, Ordinal: 0, CreatedAt: createdAt}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c.ID
}

// seedState 写一行 card_states。
func seedState(t *testing.T, db *gorm.DB, userID, cardID uint64, state string, due time.Time, stability, difficulty float64, last *time.Time) {
	t.Helper()
	row := store.CardState{UserID: userID, CardID: cardID, State: state, DueAt: &due, StepIndex: 1, ScheduledDays: 10}
	if stability > 0 {
		row.Stability = &stability
	}
	if difficulty > 0 {
		row.Difficulty = &difficulty
	}
	row.LastReviewAt = last
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create card state: %v", err)
	}
}

// seedReview 写一行 append-only 复习日志。
func seedReview(t *testing.T, db *gorm.DB, userID, cardID uint64, day string, stateBefore int, at time.Time) {
	t.Helper()
	r := store.Review{CardID: cardID, UserID: userID, Rating: int(Good), GradeSource: "self", ReviewedAt: at, ReviewDay: day, StateBefore: stateBefore}
	if err := db.Create(&r).Error; err != nil {
		t.Fatalf("create review: %v", err)
	}
}

// kindsOf 抽出队列项的优先级序列，便于断言顺序。
func kindsOf(items []QueueItem) []QueueKind {
	out := make([]QueueKind, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

// TestBuildOrdering 断言队列顺序：学习卡 → 到期复习卡 → 新卡，且复习卡可按两种方式排序。
func TestBuildOrdering(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)

	learningCard := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, learningCard, "learning", now.Add(-time.Minute), 0, 0, nil)

	// 两张复习卡：R1 稳定性低（更可能忘），R2 到期更早但稳定性高。
	lowStab := seedCard(t, db, deckID, "forward", now)
	highStab := seedCard(t, db, deckID, "forward", now)
	last := now.Add(-48 * time.Hour)
	seedState(t, db, 1, lowStab, "review", now.Add(-time.Hour), 1.0, 5.0, &last)
	seedState(t, db, 1, highStab, "review", now.Add(-2*time.Hour), 50.0, 5.0, &last)

	newIDs := []uint64{
		seedCard(t, db, deckID, "forward", now.Add(time.Minute)),
		seedCard(t, db, deckID, "forward", now.Add(2*time.Minute)),
		seedCard(t, db, deckID, "forward", now.Add(3*time.Minute)),
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, s)
	base := QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC,
		NewPerDay: 10, ReviewsPerDay: 200, NewOrder: NewOrderCreated,
	}

	ctx := context.Background()
	items, err := builder.Build(ctx, 1, withOrder(base, OrderByRetrievability))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	wantKinds := []QueueKind{QueueLearning, QueueReview, QueueReview, QueueNew, QueueNew, QueueNew}
	if got := kindsOf(items); len(got) != len(wantKinds) {
		t.Fatalf("Build() kinds = %v, want %v", got, wantKinds)
	} else {
		for i := range wantKinds {
			if got[i] != wantKinds[i] {
				t.Fatalf("Build() kinds = %v, want %v", got, wantKinds)
			}
		}
	}
	if items[0].CardID != learningCard {
		t.Errorf("first item card = %d, want learning card %d", items[0].CardID, learningCard)
	}
	// retrievability 升序：稳定性低的 R1 排在稳定性高的 R2 前。
	if items[1].CardID != lowStab || items[2].CardID != highStab {
		t.Errorf("review order = [%d %d], want [%d %d] by retrievability", items[1].CardID, items[2].CardID, lowStab, highStab)
	}
	if !(items[1].Retrievability < items[2].Retrievability) {
		t.Errorf("retrievabilities = %v, %v; want ascending", items[1].Retrievability, items[2].Retrievability)
	}
	for i, id := range newIDs {
		if items[3+i].CardID != id {
			t.Errorf("new item %d = %d, want %d (creation order)", i, items[3+i].CardID, id)
		}
	}

	// 按 due_at 升序：R2 到期更早，排在 R1 前。
	byDue, err := builder.Build(ctx, 1, withOrder(base, OrderByDueAt))
	if err != nil {
		t.Fatalf("Build(due) error = %v", err)
	}
	if byDue[1].CardID != highStab || byDue[2].CardID != lowStab {
		t.Errorf("due order = [%d %d], want [%d %d]", byDue[1].CardID, byDue[2].CardID, highStab, lowStab)
	}
}

func withOrder(o QueueOptions, order ReviewOrder) QueueOptions {
	o.ReviewOrder = order
	return o
}

// TestDailyNewCapAcrossDayBoundary 断言 03:59 / 04:00 两个瞬时点属于不同复习日，
// 因而每日新卡上限按各自已引入数正确扣减（DESIGN.md §3.3 的切点规则）。
func TestDailyNewCapAcrossDayBoundary(t *testing.T) {
	db := newTestDB(t)
	loc := time.FixedZone("test", 8*3600)
	deckID := seedDeck(t, db, time.Now().UTC())

	// 5 张新卡。
	for i := 0; i < 5; i++ {
		seedCard(t, db, deckID, string(rune('A'+i)), time.Now().UTC().Add(time.Duration(i)*time.Minute))
	}
	// 昨天（复习日 2026-10-01）已引入 2 张新卡。
	seedReview(t, db, 1, 1, "2026-10-01", int(StateNew), time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC))
	seedReview(t, db, 1, 2, "2026-10-01", int(StateNew), time.Date(2026, 10, 1, 20, 5, 0, 0, time.UTC))

	if got := ReviewDay(time.Date(2026, 10, 2, 3, 59, 0, 0, loc), loc, 4); got != "2026-10-01" {
		t.Fatalf("ReviewDay(03:59) = %q, want 2026-10-01", got)
	}
	if got := ReviewDay(time.Date(2026, 10, 2, 4, 0, 0, 0, loc), loc, 4); got != "2026-10-02" {
		t.Fatalf("ReviewDay(04:00) = %q, want 2026-10-02", got)
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, s)
	newCount := func(now time.Time) int {
		t.Helper()
		items, err := builder.Build(context.Background(), 1, QueueOptions{
			DeckID: deckID, Now: now, Location: loc, NewPerDay: 3, ReviewsPerDay: 200, NewOrder: NewOrderCreated,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		n := 0
		for _, it := range items {
			if it.Kind == QueueNew {
				n++
			}
		}
		return n
	}

	if got := newCount(time.Date(2026, 10, 2, 3, 59, 0, 0, loc)); got != 1 {
		t.Errorf("new cards at 03:59 = %d, want 1 (3 cap - 2 already introduced on 2026-10-01)", got)
	}
	if got := newCount(time.Date(2026, 10, 2, 4, 0, 0, 0, loc)); got != 3 {
		t.Errorf("new cards at 04:00 = %d, want 3 (fresh review day)", got)
	}
}

// TestReviewsPerDayCap 断言每日复习上限来自 reviews 聚合：0 表示不限。
func TestReviewsPerDayCap(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	for i := 0; i < 3; i++ {
		id := seedCard(t, db, deckID, string(rune('A'+i)), now)
		last := now.Add(-24 * time.Hour)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}

	s := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, s)
	ctx := context.Background()

	capped, err := builder.Build(ctx, 1, QueueOptions{DeckID: deckID, Now: now, Location: time.UTC, ReviewsPerDay: 1})
	if err != nil {
		t.Fatalf("Build(cap=1) error = %v", err)
	}
	if len(capped) != 1 || capped[0].Kind != QueueReview {
		t.Fatalf("Build(cap=1) = %+v, want exactly 1 review card", capped)
	}

	unlimited, err := builder.Build(ctx, 1, QueueOptions{DeckID: deckID, Now: now, Location: time.UTC, ReviewsPerDay: 0})
	if err != nil {
		t.Fatalf("Build(cap=0) error = %v", err)
	}
	if len(unlimited) != 3 {
		t.Errorf("Build(cap=0) returned %d cards, want 3 (unlimited)", len(unlimited))
	}
}

// TestQueueExcludesSuspendedAndDeleted 断言暂停卡与软删除 note 的卡不出现在队列里。
func TestQueueExcludesSuspendedAndDeleted(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)

	visible := seedCard(t, db, deckID, "forward", now)
	last := now.Add(-24 * time.Hour)
	seedState(t, db, 1, visible, "review", now.Add(-time.Hour), 5.0, 5.0, &last)

	suspended := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, suspended, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	if err := db.Model(&store.Card{}).Where("id = ?", suspended).Update("suspended_at", now).Error; err != nil {
		t.Fatalf("suspend card: %v", err)
	}

	deleted := seedCard(t, db, deckID, "forward", now)
	seedState(t, db, 1, deleted, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	if err := db.Where("id = ?", deleted).Delete(&store.Card{}).Error; err != nil {
		t.Fatalf("delete card: %v", err)
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, s).Build(context.Background(), 1, QueueOptions{DeckID: deckID, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(items) != 1 || items[0].CardID != visible {
		t.Errorf("Build() = %+v, want only the visible card %d", items, visible)
	}
}
