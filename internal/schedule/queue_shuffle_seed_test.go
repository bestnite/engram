package schedule

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// 本文件钉住新卡乱序的**确定性**：种子由 (用户, 复习日) 派生，而不是构建时刻。
//
// 修复前的形态是 `rand.New(rand.NewSource(now.UnixNano()))`，而生产调用方从不设置
// QueueOptions.Rand，于是同一天里每刷新一次页面，新卡顺序就换一遍——学习者看到的顺序不稳定，
// 报告问题时也无从复现。

// buildNewOrder 建一个含 n 张新卡的卡组，返回本次构建里新卡的顺序。
func buildNewOrder(t *testing.T, userID uint64, now time.Time, n int) []uint64 {
	t.Helper()
	db := newTestDB(t)
	deckID := seedDeck(t, db, now)
	for i := 0; i < n; i++ {
		seedCard(t, db, deckID, "forward", now.Add(time.Duration(i)*time.Minute))
	}
	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	opts := QueueOptions{
		DeckID: deckID, Now: now, Location: time.UTC,
		NewPerDay: n + 1, ReviewsPerDay: 0, NewOrder: NewOrderRandom,
	}
	items, err := builder.Build(context.Background(), userID, opts)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	out := make([]uint64, 0, len(items))
	for _, it := range items {
		if it.Kind == QueueNew {
			out = append(out, it.CardID)
		}
	}
	if len(out) != n {
		t.Fatalf("built %d new cards, want %d", len(out), n)
	}
	return out
}

// TestNewOrderIsStableWithinTheSameReviewDay 断言同一天内反复构建（哪怕时刻不同、分钟不同）
// 得到完全相同的排列——这就是「顺序稳定、可复现」的含义。
func TestNewOrderIsStableWithinTheSameReviewDay(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	first := buildNewOrder(t, 1, now, 12)
	// 同一天、切点 04:00 之内的另一个时刻（含纳秒级差异）。
	later := buildNewOrder(t, 1, now.Add(7*time.Hour+123*time.Millisecond), 12)
	if len(first) != len(later) {
		t.Fatalf("lengths differ: %d vs %d", len(first), len(later))
	}
	for i := range first {
		if first[i] != later[i] {
			t.Fatalf("new-card order changed within one review day: %v vs %v", first, later)
		}
	}
}

// TestNewOrderDiffersAcrossReviewDays 断言跨复习日会换一个排列（避免永远只背开头几张）。
//
// 种子是输入的纯函数，所以这个断言是确定性的：同样的两天永远得到同样的结论，不会偶发翻转。
func TestNewOrderDiffersAcrossReviewDays(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	day1 := buildNewOrder(t, 1, now, 12)
	day2 := buildNewOrder(t, 1, now.Add(24*time.Hour), 12)
	if sameOrder(day1, day2) {
		t.Errorf("the same permutation on two review days: %v", day1)
	}
}

// TestNewOrderDiffersAcrossUsers 断言两个用户同一天不共享同一排列（否则用户之间会开始
// 以相同顺序相遇同一批卡，削弱「打乱」的观感）。
func TestNewOrderDiffersAcrossUsers(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	a := buildNewOrder(t, 1, now, 12)
	b := buildNewOrder(t, 2, now, 12)
	if sameOrder(a, b) {
		t.Errorf("two users got the same permutation on the same day: %v", a)
	}
}

// TestExplicitRandStillWins 断言显式传入的随机源不被替换（测试与显式入口的契约）。
func TestExplicitRandStillWins(t *testing.T) {
	db := newTestDB(t)
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	deckID := seedDeck(t, db, now)
	const n = 12
	for i := 0; i < n; i++ {
		seedCard(t, db, deckID, "forward", now.Add(time.Duration(i)*time.Minute))
	}
	builder := NewQueueBuilder(db, store.NewDeckStore(db), mustScheduler(t, testPreset(t)))
	// 两次构建共用同一个显式随机源：第二次接着上一次的序列，因此顺序不同。
	rng := rand.New(rand.NewSource(42))
	order := func() []uint64 {
		items, err := builder.Build(context.Background(), 1, QueueOptions{
			DeckID: deckID, Now: now, Location: time.UTC,
			NewPerDay: n + 1, ReviewsPerDay: 0, NewOrder: NewOrderRandom, Rand: rng,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		out := make([]uint64, 0, n)
		for _, it := range items {
			if it.Kind == QueueNew {
				out = append(out, it.CardID)
			}
		}
		return out
	}
	if sameOrder(order(), order()) {
		t.Error("an explicit Rand should be consumed as-is, not ignored")
	}
}

// sameOrder 比较两个顺序是否逐位相同。
func sameOrder(a, b []uint64) bool {
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
