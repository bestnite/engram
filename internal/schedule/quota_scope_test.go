package schedule

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// TestNewCardQuotaIsCountedPerDeck 是额度口径的验收 把 new_per_day 定义为
// **卡组级**设置，所以单卡组范围内"今日已引入"只能数该卡组的记录。
//
// 修复前它按全库统计：一个卡组刷满新卡额度会把另一个卡组的新卡一起挡住，用户实测到的症状
// 是"卡组列表显示 6 张到期，点进去却说当前没有到期的卡片"。现在全库/多卡组范围也按各卡组
// 自己的额度求和（new_per_day 是卡组级列）。
func TestNewCardQuotaIsCountedPerDeck(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	day := ReviewDay(now, time.UTC, DefaultDayCutoffHour)

	// 两个卡组，各把每日新卡上限设为 1。
	deckA := seedDeckWithNewCap(t, db, now, "A", 1)
	deckB := seedDeckWithNewCap(t, db, now, "B", 1)

	// A 卡组今天已经引入 1 张新卡（额度用尽）；两个卡组各还剩 1 张新卡。
	// 已引入那张要有状态行（learning、到期在未来），否则它仍是"新卡"、会再次入队。
	usedInA := seedCard(t, db, deckA, "used", now)
	seedReview(t, db, 1, usedInA, day, int(StateNew), now.Add(-time.Hour))
	seedState(t, db, 1, usedInA, "learning", now.Add(time.Hour), 1.0, 5.0, nil)
	seedCard(t, db, deckA, "freshA", now.Add(time.Minute))
	seedCard(t, db, deckB, "freshB", now.Add(2*time.Minute))

	s := mustScheduler(t, testPreset(t))
	build := func(opts QueueOptions) []QueueItem {
		t.Helper()
		opts.Now = now
		opts.Location = time.UTC
		opts.NewOrder = NewOrderCreated
		items, err := NewQueueBuilder(db, store.NewDeckStore(db), s).Build(context.Background(), 1, opts)
		if err != nil {
			t.Fatalf("Build(%+v) error = %v", opts, err)
		}
		return items
	}

	// B 卡组：A 用掉的额度不该算在它头上 —— 必须出它自己那张新卡。
	if got := build(QueueOptions{DeckID: deckB}); len(got) != 1 || got[0].DeckID != deckB {
		t.Errorf("deck B queue = %+v, want exactly its own new card (deck A's usage must not count)", got)
	}
	// A 卡组：自己的额度已用尽 —— 一张新卡都不出。
	if got := build(QueueOptions{DeckID: deckA}); len(got) != 0 {
		t.Errorf("deck A queue = %+v, want empty (its own daily new-card cap is used up)", got)
	}
	// 全库范围：按各卡组自己的额度求和 —— B 出 1 张、A 额度已用尽出 0 张。
	if got := build(QueueOptions{}); len(got) != 1 || got[0].DeckID != deckB {
		t.Errorf("all-decks queue = %+v, want only deck B's 1 new card (per-deck budgets summed)", got)
	}
}

// seedDeckWithNewCap 建一个预设 + 私有卡组，并显式设置每日新卡上限。
func seedDeckWithNewCap(t *testing.T, db *gorm.DB, now time.Time, name string, newPerDay int) uint64 {
	t.Helper()
	p := store.NewPreset(1, "preset-"+name)
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{
		OwnerUserID: 1, Name: name, Description: "", Visibility: store.DeckVisibilityPrivate,
		NewPerDay: newPerDay, ReviewsPerDay: 200, PresetID: p.ID, CreatedAt: now,
	}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	return d.ID
}
