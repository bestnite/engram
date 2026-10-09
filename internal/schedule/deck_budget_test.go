package schedule

import (
	"context"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestDeckBudgetsReportsUsedAndLeft 是设置页「今日已用 / 剩余」的数据源验收：
// DeckBudgets 必须与队列同源（复用 deckBudget 的公式），并且用布尔量而不是 -1/0
// 表达「不限」。
//
// 夹具覆盖三种边界：
//
//	(a) 卡组列写 0（不限）：PerDay 为 0、Unlimited 为 true，即使今日已用也不封顶；
//	(b) 已用等于上限 → Left 0；已用超过上限 → 仍为 0，不出现负数；
//	(c) 没有任何 card_states / reviews 的卡组：今日已用为 0，Left 等于 PerDay。
func TestDeckBudgetsReportsUsedAndLeft(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := ReviewDay(now, time.UTC, DefaultDayCutoffHour)
	at := now.Add(-time.Hour)
	future := now.Add(48 * time.Hour)
	last := now.Add(-48 * time.Hour)

	decks := store.NewDeckStore(db)
	mkDeck := func(name string, newPer, reviewPer int) uint64 {
		t.Helper()
		p := store.NewPreset(1, "preset-"+name)
		p.EnableFuzz = boolPtr(false)
		if err := db.Create(&p).Error; err != nil {
			t.Fatalf("create preset: %v", err)
		}
		d := store.Deck{OwnerUserID: 1, Name: name, PresetID: p.ID, CreatedAt: now}
		if err := db.Create(&d).Error; err != nil {
			t.Fatalf("create deck %s: %v", name, err)
		}
		// 用 map 写上限：0 在这里合法（不限），模型整体 Save 会省略带默认值列的 0。
		if err := db.Model(&store.Deck{}).Where("id = ?", d.ID).
			Updates(map[string]any{"new_per_day": newPer, "reviews_per_day": reviewPer}).Error; err != nil {
			t.Fatalf("set caps on deck %s: %v", name, err)
		}
		return d.ID
	}
	// usedNew 记 n 次「今日引入新卡」（state_before = 0）。
	usedNew := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "new-used", now)
			seedReview(t, db, 1, id, day, int(StateNew), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}
	// usedReview 记 n 次「今日复习」（state_before <> 0）。
	usedReview := func(deckID uint64, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			id := seedCard(t, db, deckID, "review-used", now)
			seedReview(t, db, 1, id, day, int(StateReview), at)
			seedState(t, db, 1, id, "review", future, 5.0, 5.0, &last)
		}
	}

	// (a) 0/0 表示不限：不封顶，两个 Unlimited 都为 true。
	unlimited := mkDeck("unlimited", 0, 0)
	usedNew(unlimited, 7)
	usedReview(unlimited, 9)

	// (b1) 已用等于上限；b2 已用超过上限（复习用了 5，上限 3）。
	atCap := mkDeck("at-cap", 2, 3)
	usedNew(atCap, 2)
	usedReview(atCap, 3)
	overCap := mkDeck("over-cap", 5, 3)
	usedNew(overCap, 1)
	usedReview(overCap, 5)

	// (c) 完全没用过的卡组：已用为 0，剩余等于上限。
	untouched := mkDeck("untouched", 5, 10)

	sched := mustScheduler(t, testPreset(t))
	builder := NewQueueBuilder(db, decks, sched)

	budgets, err := builder.DeckBudgets(ctx, 1, []uint64{unlimited, atCap, overCap, untouched})
	if err != nil {
		t.Fatalf("DeckBudgets() error = %v", err)
	}
	if len(budgets) != 4 {
		t.Fatalf("DeckBudgets returned %d decks, want 4", len(budgets))
	}

	// (a) 不限。
	u := budgets[unlimited]
	if !u.NewUnlimited || !u.ReviewUnlimited {
		t.Errorf("unlimited deck: NewUnlimited=%v ReviewUnlimited=%v, want both true for caps 0/0", u.NewUnlimited, u.ReviewUnlimited)
	}
	if u.NewPerDay != 0 || u.ReviewsPerDay != 0 {
		t.Errorf("unlimited deck: PerDay = %d/%d, want 0/0", u.NewPerDay, u.ReviewsPerDay)
	}
	if u.NewUsed != 7 || u.ReviewUsed != 9 {
		t.Errorf("unlimited deck: Used = %d/%d, want 7/9 (usage is still reported when unlimited)", u.NewUsed, u.ReviewUsed)
	}

	// (b1) 已用等于上限。
	eq := budgets[atCap]
	if eq.NewUnlimited || eq.ReviewUnlimited {
		t.Errorf("at-cap deck: Unlimited flags = %v/%v, want false", eq.NewUnlimited, eq.ReviewUnlimited)
	}
	if eq.NewUsed != 2 || eq.ReviewUsed != 3 {
		t.Errorf("at-cap deck: Used = %d/%d, want 2/3", eq.NewUsed, eq.ReviewUsed)
	}
	if eq.NewLeft != 0 || eq.ReviewLeft != 0 {
		t.Errorf("at-cap deck: Left = %d/%d, want 0/0 when usage equals the cap", eq.NewLeft, eq.ReviewLeft)
	}

	// (b2) 已用超过上限：剩余仍为 0，绝不出现负数。
	over := budgets[overCap]
	if over.ReviewUsed != 5 || over.ReviewLeft != 0 {
		t.Errorf("over-cap deck: ReviewUsed=%d ReviewLeft=%d, want 5/0 (never negative)", over.ReviewUsed, over.ReviewLeft)
	}
	if over.NewUsed != 1 || over.NewLeft != 4 {
		t.Errorf("over-cap deck: NewUsed=%d NewLeft=%d, want 1/4", over.NewUsed, over.NewLeft)
	}

	// (c) 没动过的卡组：已用 0，剩余等于上限。
	ut := budgets[untouched]
	if ut.NewUsed != 0 || ut.ReviewUsed != 0 {
		t.Errorf("untouched deck: Used = %d/%d, want 0/0", ut.NewUsed, ut.ReviewUsed)
	}
	if ut.NewLeft != 5 || ut.ReviewLeft != 10 {
		t.Errorf("untouched deck: Left = %d/%d, want 5/10", ut.NewLeft, ut.ReviewLeft)
	}
	if ut.NewUnlimited || ut.ReviewUnlimited {
		t.Errorf("untouched deck: Unlimited flags = %v/%v, want false", ut.NewUnlimited, ut.ReviewUnlimited)
	}

	// 不存在的卡组 id 沿用 resolveScope 的兜底：新卡上限取默认，复习不限。
	missing, err := builder.DeckBudgets(ctx, 1, []uint64{99999})
	if err != nil {
		t.Fatalf("DeckBudgets(missing) error = %v", err)
	}
	if got := missing[99999]; got.NewPerDay != DefaultNewPerDay || !got.ReviewUnlimited {
		t.Errorf("missing deck: %+v, want NewPerDay=%d and ReviewUnlimited=true (documented fallback)", got, DefaultNewPerDay)
	}

	// 空集合与非零 userID 校验。
	empty, err := builder.DeckBudgets(ctx, 1, nil)
	if err != nil {
		t.Fatalf("DeckBudgets(nil) error = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("DeckBudgets(nil) returned %d entries, want 0", len(empty))
	}
	if _, err := builder.DeckBudgets(ctx, 0, []uint64{unlimited}); err == nil {
		t.Error("DeckBudgets(userID=0) error = nil, want an error")
	}
}
