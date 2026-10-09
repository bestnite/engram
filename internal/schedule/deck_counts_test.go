package schedule

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckCountsMatchBuildPerDeck 是本次额度口径重构的验收核心（属性测试）：
// 对夹具里每个可见卡组，DeckCounts[d] 必须等于 Build(DeckID=d) 里 QueueNew / QueueReview
// 两种 kind 的条数。DeckCounts 与 Build 共用同一条取卡路径（collect）与同一条额度公式
// （deckBudget），所以列表页的「新 X · 复习 Y」相加就是点进去能刷的张数。
//
// 夹具覆盖：
//
//	(a) 今日新卡额度用尽（该卡组 新 0，但学习卡仍出队）；
//	(b) 多卡组范围按各卡组自己的额度求和（两个 20/20 的卡组，已用分布不同）；
//	(c) 卡组列 new_per_day=0 / reviews_per_day=0（不限）；
//	(d) 负例：另一个用户的卡组（含新卡）不出现，无论它叫什么。
//
// DeckCounts 无 Now 参数，内部用当刻时钟；夹具里"今日已用"的复习行按 now 的复习日写入，
// 与 Build 传入的 Now 同一天（两者相隔毫秒，除跨 04:00 切点外一致）。
func TestDeckCountsMatchBuildPerDeck(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := ReviewDay(now, time.UTC, DefaultDayCutoffHour)
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)
	at := now.Add(-time.Hour)

	decks := store.NewDeckStore(db)
	mkDeck := func(owner uint64, name string, newPer, reviewPer int) uint64 {
		t.Helper()
		p := store.NewPreset(owner, "preset-"+name)
		p.EnableFuzz = boolPtr(false)
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
		d := store.Deck{OwnerUserID: owner, Name: name, PresetID: p.ID, CreatedAt: now}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		// 用 map 更新写上限：0 在这里合法（不限），模型整体 Save 会把带默认值列的 0 省略。
		if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
			Updates(map[string]any{"new_per_day": newPer, "reviews_per_day": reviewPer}).Error; err != nil {
			t.Fatalf("set caps on deck %s: %v", name, err)
		}
		return d.ID
	}
	dueReview := func(deckID uint64) {
		t.Helper()
		id := seedCard(t, db, deckID, "forward", now)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}
	// introduced 写入 n 张"今日已引入"的新卡：复习行 state_before=0 + 一行不出的状态。
	introduced := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "used", now)
			seedReview(t, db, 1, id, day, int(StateNew), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}
	// reviewedToday 写入 n 次"今日的复习"（state_before<>0），用于制造各不相同的已用量。
	reviewedToday := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "reviewed", now)
			seedReview(t, db, 1, id, day, int(StateReview), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}
	seedNew := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			seedCard(t, db, deckID, "new", now.Add(time.Duration(i)*time.Minute))
		}
	}

	// (a) 新卡额度 1，今日已用尽；另有一张到期的学习卡（不受额度裁剪）。
	quota := mkDeck(1, "quota-used", 1, 200)
	introduced(quota, 1)
	seedNew(quota, 2)
	learning := seedCard(t, db, quota, "learning", now)
	seedState(t, db, 1, learning, "learning", now.Add(-time.Minute), 0, 0, nil)
	dueReview(quota)

	// (b) 20/20 的两个卡组，已用分布不同：limited 用 0，partial 用 5 新 / 8 复习。
	limited := mkDeck(1, "limited", 20, 20)
	seedNew(limited, 25)
	for i := 0; i < 25; i++ {
		dueReview(limited)
	}
	partial := mkDeck(1, "partial", 20, 20)
	introduced(partial, 5)
	reviewedToday(partial, 8)
	seedNew(partial, 25)
	for i := 0; i < 25; i++ {
		dueReview(partial)
	}

	// (c) 0 表示不限。
	unlimited := mkDeck(1, "unlimited", 0, 0)
	seedNew(unlimited, 4)
	for i := 0; i < 4; i++ {
		dueReview(unlimited)
	}

	// (d) 别的用户的两张卡组各含新卡，都不该出现：没有授权就没有可见性。
	otherPrivate := mkDeck(2, "other-private", 20, 200)
	seedNew(otherPrivate, 5)
	otherDeck := mkDeck(2, "other-deck", 20, 200)
	seedNew(otherDeck, 2)

	sched := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, decks, sched)

	visible, err := decks.VisibleIDs(ctx, 1)
	if err != nil {
		t.Fatalf("VisibleIDs() error = %v", err)
	}
	if len(visible) != 4 {
		t.Fatalf("visible decks = %v, want 4 (owned only: another user's decks are never visible)", visible)
	}
	for _, id := range visible {
		if id == otherPrivate || id == otherDeck {
			t.Errorf("visible set leaked another user's deck %d", id)
		}
	}

	counts, err := builder.DeckCounts(ctx, 1, visible, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckCounts() error = %v", err)
	}
	if len(counts) != len(visible) {
		t.Errorf("DeckCounts returned %d decks, want %d", len(counts), len(visible))
	}

	// 属性：逐卡组与 Build(DeckID=d) 的张数相等 —— 列表上的「新」＝QueueNew，
	// 「复习」＝学习/再学习卡 + 到期复习卡，两者相加就是点进去能刷的张数。
	for _, id := range visible {
		items, err := builder.Build(ctx, 1, QueueOptions{DeckID: id, Now: now, Location: time.UTC})
		if err != nil {
			t.Fatalf("Build(DeckID=%d) error = %v", id, err)
		}
		newN := countKind(items, QueueNew)
		reviewN := countKind(items, QueueReview) + countKind(items, QueueLearning)
		if got := counts[id]; got.New != newN || got.Review != reviewN {
			t.Errorf("deck %d: DeckCounts = %+v, Build(DeckID) New=%d Review(learning+due)=%d", id, got, newN, reviewN)
		}
	}

	// (a) 额度用尽：新 0；复习 2 = 1 张到期的学习卡 + 1 张到期的复习卡。
	if got := counts[quota]; got.New != 0 || got.Review != 2 {
		t.Errorf("quota deck counts = %+v, want New=0 Review=2 (1 learning + 1 due review)", got)
	}
	qitems, err := builder.Build(ctx, 1, QueueOptions{DeckID: quota, Now: now, Location: time.UTC})
	if err != nil {
		t.Fatalf("Build(DeckID=quota) error = %v", err)
	}
	if n := countKind(qitems, QueueLearning); n != 1 {
		t.Errorf("quota deck learning cards = %d, want 1 (learning cards are never capped)", n)
	}

	// (b) 各卡组自己的额度。
	if got := counts[limited]; got.New != 20 || got.Review != 20 {
		t.Errorf("limited deck counts = %+v, want New=20 Review=20", got)
	}
	if got := counts[partial]; got.New != 15 || got.Review != 12 {
		t.Errorf("partial deck counts = %+v, want New=15 Review=12 (20 minus today's usage)", got)
	}

	// (c) 0 表示不限。
	if got := counts[unlimited]; got.New != 4 || got.Review != 4 {
		t.Errorf("unlimited deck counts = %+v, want New=4 Review=4 (0 means unlimited)", got)
	}

	// (d) 全库口径：只含可见卡组，别人的卡组一张都不出现。
	all, err := builder.Build(ctx, 1, QueueOptions{Now: now, Location: time.UTC, NewOrder: NewOrderCreated})
	if err != nil {
		t.Fatalf("Build(all decks) error = %v", err)
	}
	for _, it := range all {
		if it.DeckID == otherPrivate || it.DeckID == otherDeck {
			t.Errorf("all-decks queue leaked another user's deck %d", it.DeckID)
		}
	}
	allCounts, err := builder.DeckCounts(ctx, 1, nil, QueueOptions{})
	if err != nil {
		t.Fatalf("DeckCounts(nil) error = %v", err)
	}
	if len(allCounts) != len(counts) {
		t.Errorf("all-decks DeckCounts size = %d, want %d", len(allCounts), len(counts))
	}
	for id, want := range counts {
		if allCounts[id] != want {
			t.Errorf("deck %d: all-decks counts = %+v, want %+v", id, allCounts[id], want)
		}
	}
	if got, ok := allCounts[otherDeck]; ok {
		t.Errorf("all-decks counts include another user's deck %d = %+v, want absent", otherDeck, got)
	}
}

// containsID 报告集合里是否有给定 id。
func containsID(ids []uint64, want uint64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestDeckCountsUnlimitedIsNotCappedByBatch 断言不限量卡组的复习数是真实张数：
// 单批取卡上限（BatchSize）只决定一次返回多少，不决定今天能刷多少。
func TestDeckCountsUnlimitedIsNotCappedByBatch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	last := now.Add(-48 * time.Hour)
	p := store.NewPreset(1, "preset")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: 1, Name: "big", PresetID: p.ID, CreatedAt: now}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
		Updates(map[string]any{"new_per_day": 0, "reviews_per_day": 0}).Error; err != nil {
		t.Fatalf("set caps: %v", err)
	}
	const due, fresh = DefaultReviewBatch + 50, 30
	for i := 0; i < due; i++ {
		id := seedCard(t, db, d.ID, "forward", now)
		seedState(t, db, 1, id, "review", now.Add(-time.Hour), 5.0, 5.0, &last)
	}
	for i := 0; i < fresh; i++ {
		seedCard(t, db, d.ID, "forward", now)
	}
	counts, err := NewQueueBuilder(db, store.NewDeckStore(db), nil).DeckCounts(ctx, 1, []uint64{d.ID}, QueueOptions{Now: now})
	if err != nil {
		t.Fatalf("DeckCounts() error = %v", err)
	}
	if got := counts[d.ID]; got.Review != due || got.New != fresh {
		t.Errorf("counts = %+v, want Review %d New %d", got, due, fresh)
	}
}

// TestDeckCountsUseTheUsersReviewDay 断言「今日已用量」按用户时区的复习日统计。
// 01:00 UTC 在 UTC+8 已是 09:00，用户的复习日是 10-07；按 UTC 算则仍是 10-06。
// 当天已引入 1 张、上限 1 张时，用户口径下新卡额度用完（0）；若误用 UTC 口径就会显示 1。
func TestDeckCountsUseTheUsersReviewDay(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	p := store.NewPreset(1, "preset")
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create preset: %v", err)
	}
	d := store.Deck{OwnerUserID: 1, Name: "tz", PresetID: p.ID, CreatedAt: now}
	if err := db.Create(&d).Error; err != nil {
		t.Fatalf("create deck: %v", err)
	}
	if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
		Updates(map[string]any{"new_per_day": 1, "reviews_per_day": 0}).Error; err != nil {
		t.Fatalf("set caps: %v", err)
	}
	userDay := ReviewDay(now, shanghai, DefaultDayCutoffHour)
	if utcDay := ReviewDay(now, time.UTC, DefaultDayCutoffHour); utcDay == userDay {
		t.Fatalf("fixture error: user day %s equals UTC day", userDay)
	}
	used := seedCard(t, db, d.ID, "used", now)
	seedReview(t, db, 1, used, userDay, int(StateNew), now.Add(-time.Minute))
	future := now.Add(48 * time.Hour)
	seedState(t, db, 1, used, "review", future, 5.0, 5.0, &now)
	seedCard(t, db, d.ID, "forward", now)

	builder := NewQueueBuilder(db, store.NewDeckStore(db), nil)
	cases := []struct {
		name    string
		tz      string
		wantNew int
	}{
		{"user timezone sees today's quota used", "Asia/Shanghai", 0},
		{"UTC reading is a different day", "UTC", 1},
	}
	for _, tc := range cases {
		counts, err := builder.DeckCounts(ctx, 1, []uint64{d.ID}, QueueOptions{Now: now, Timezone: tc.tz})
		if err != nil {
			t.Fatalf("%s: DeckCounts() error = %v", tc.name, err)
		}
		if got := counts[d.ID].New; got != tc.wantNew {
			t.Errorf("%s: New = %d, want %d", tc.name, got, tc.wantNew)
		}
	}
}
