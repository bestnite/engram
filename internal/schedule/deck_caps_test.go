package schedule

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestQueueHonoursDeckDailyCaps 是 M3-8 的验收用例：每日上限必须来自卡组列，
// 而不是 QueueOptions 里的硬编码默认值。卡组设成 2/1（远低于默认 20/200），
// 若仍走默认值，下面的断言会立刻失败。
func TestQueueHonoursDeckDailyCaps(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	p := store.NewPreset(1, "preset")
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: 1, Name: "Capped", Description: "", Visibility: store.DeckVisibilityPrivate,
		NewPerDay: 2, ReviewsPerDay: 1, PresetID: p.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}

	// 3 张到期复习卡。
	for i := 0; i < 3; i++ {
		id := seedCard(t, db, deck.ID, string(rune('A'+i)), now)
		last := now.Add(-24 * time.Hour)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}
	// 5 张新卡。
	for i := 0; i < 5; i++ {
		seedCard(t, db, deck.ID, string(rune('a'+i)), now.Add(time.Duration(i)*time.Minute))
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, s).Build(context.Background(), 1, QueueOptions{
		DeckID: deck.ID, Now: now, Location: time.UTC, NewOrder: NewOrderCreated,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	reviews, news := 0, 0
	for _, it := range items {
		switch it.Kind {
		case QueueReview:
			reviews++
		case QueueNew:
			news++
		}
	}
	if reviews != 1 {
		t.Errorf("review cards = %d, want 1 (deck reviews_per_day)", reviews)
	}
	if news != 2 {
		t.Errorf("new cards = %d, want 2 (deck new_per_day)", news)
	}
}

// TestQueueCapsExplicitOverrideBeatsDeck 断言显式覆盖仍然优先于卡组设置，
// 现有调用方（传正值的 NewPerDay / ReviewsPerDay）行为不变。
func TestQueueCapsExplicitOverrideBeatsDeck(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	p := store.NewPreset(1, "preset")
	p.EnableFuzz = boolPtr(false)
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	deck := store.Deck{
		OwnerUserID: 1, Name: "Capped", Visibility: store.DeckVisibilityPrivate,
		NewPerDay: 2, ReviewsPerDay: 1, PresetID: p.ID, CreatedAt: now,
	}
	if err := db.Create(&deck).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	for i := 0; i < 5; i++ {
		seedCard(t, db, deck.ID, string(rune('a'+i)), now.Add(time.Duration(i)*time.Minute))
	}

	s := mustScheduler(t, testPreset(t))
	items, err := NewQueueBuilder(db, s).Build(context.Background(), 1, QueueOptions{
		DeckID: deck.ID, Now: now, Location: time.UTC, NewOrder: NewOrderCreated,
		NewPerDay: 5,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	news := 0
	for _, it := range items {
		if it.Kind == QueueNew {
			news++
		}
	}
	if news != 5 {
		t.Errorf("new cards = %d, want 5 (explicit override beats the deck cap of 2)", news)
	}
}
