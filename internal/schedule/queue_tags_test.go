package schedule

import (
	"context"
	"sort"
	"testing"
	"time"

	"gorm.io/gorm"

	"git.nite07.com/nite/engram/internal/store"
)

// seedTaggedCard 建一个带标签的 basic note 与它的一张 card，返回 card id。
func seedTaggedCard(t *testing.T, db *gorm.DB, deckID uint64, tags []string, createdAt time.Time) uint64 {
	t.Helper()
	n := store.Note{DeckID: deckID, Kind: "basic", FieldsJSON: `{"front":"q","back":"a"}`,
		TagsJSON: store.TagsJSON(tags), CreatedAt: createdAt, UpdatedAt: createdAt}
	if err := db.Create(&n).Error; err != nil {
		t.Fatalf("create note: %v", err)
	}
	c := store.Card{NoteID: n.ID, Template: "forward", Ordinal: 0, CreatedAt: createdAt}
	if err := db.Create(&c).Error; err != nil {
		t.Fatalf("create card: %v", err)
	}
	return c.ID
}

func sortedCardIDs(items []QueueItem) []uint64 {
	ids := make([]uint64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.CardID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedIDs(ids ...uint64) []uint64 {
	out := append([]uint64{}, ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalIDs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildFiltersByTags 断言 Tags 对学习卡、到期复习卡、新卡与提前学习卡同时生效，
// 多个标签取并集，空 Tags 不过滤。
func TestBuildFiltersByTags(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	last := now.Add(-24 * time.Hour)

	culture := []string{"文化传承"}
	innov := []string{"创新"}
	newCulture := seedTaggedCard(t, db, deckID, culture, now)
	newInnov := seedTaggedCard(t, db, deckID, innov, now)
	newPlain := seedTaggedCard(t, db, deckID, nil, now)
	reviewCulture := seedTaggedCard(t, db, deckID, culture, now)
	seedState(t, db, 1, reviewCulture, "review", now.Add(-time.Hour), 5, 5, &last)
	reviewInnov := seedTaggedCard(t, db, deckID, []string{"创新", "高频"}, now)
	seedState(t, db, 1, reviewInnov, "review", now.Add(-time.Hour), 5, 5, &last)
	learnCulture := seedTaggedCard(t, db, deckID, culture, now)
	seedState(t, db, 1, learnCulture, "learning", now.Add(-time.Minute), 1, 5, &last)
	aheadInnov := seedTaggedCard(t, db, deckID, innov, now)
	seedState(t, db, 1, aheadInnov, "learning", now.Add(5*time.Minute), 1, 5, &last)

	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	cases := []struct {
		name string
		tags []string
		want []uint64
	}{
		{"no tags means whole deck", nil, sortedIDs(newCulture, newInnov, newPlain, reviewCulture, reviewInnov, learnCulture, aheadInnov)},
		{"one tag", culture, sortedIDs(newCulture, reviewCulture, learnCulture)},
		{"tag on a multi-tag note, learn-ahead included", innov, sortedIDs(newInnov, reviewInnov, aheadInnov)},
		{"union of tags", []string{"文化传承", "高频"}, sortedIDs(newCulture, reviewCulture, learnCulture, reviewInnov)},
		{"unknown tag yields empty queue", []string{"不存在"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := builder.Build(context.Background(), 1, QueueOptions{
				DeckID: deckID, Now: now, Location: time.UTC, NewOrder: NewOrderCreated,
				LearnAhead: 20 * time.Minute, Tags: tc.tags,
			})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if got := sortedCardIDs(items); !equalIDs(got, tc.want) {
				t.Fatalf("Build(tags=%q) cards = %v, want %v", tc.tags, got, tc.want)
			}
		})
	}
}

// TestTagQueueSharesDeckBudget 断言标签队列与整卡组队列共用卡组的每日额度：今天在别的标签里
// 引入的新卡同样扣减本卡组的 new_per_day，标签不会开出第二份额度。
func TestTagQueueSharesDeckBudget(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	culture := []string{"文化传承"}
	for i := 0; i < 3; i++ {
		seedTaggedCard(t, db, deckID, culture, now.Add(time.Duration(i)*time.Minute))
	}
	// 今天已在「创新」标签里引入 1 张新卡（state_before = New 的复习日志）。
	introduced := seedTaggedCard(t, db, deckID, []string{"创新"}, now)
	seedReview(t, db, 1, introduced, ReviewDay(now, time.UTC, 4), int(StateNew), now.Add(-time.Hour))

	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	items, err := builder.Build(context.Background(), 1, QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC, NewOrder: NewOrderCreated,
		NewPerDay: 2, Tags: culture,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := countKind(items, QueueNew); got != 1 {
		t.Fatalf("new cards under tag = %d, want 1 (cap 2 minus 1 already introduced in the deck today)", got)
	}
}
