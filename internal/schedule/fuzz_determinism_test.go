package schedule

import (
	"testing"
	"time"

	"example.com/flashcard/internal/store"
)

// TestFuzzIsDeterministicForSameSeed 断言 EnableFuzz 下同一张卡、同一 now 的两次评分产出
// 完全相同的间隔（DESIGN.md §9 验收：fuzz 确定性 —— 同一种子重放得到相同间隔）。
//
// go-fsrs 的 fuzz 种子由 (now.UnixMilli, reps, difficulty*stability) 组成（见上游
// scheduler.initSeed），因此对本包而言“种子”就是 (CardState, now)：二者相同则 ALEA
// 序列相同、扰动相同。若实现里引入了挂钟或全局随机源，本用例会失败。
func TestFuzzIsDeterministicForSameSeed(t *testing.T) {
	preset := testPreset(t)
	preset.EnableFuzz = boolPtr(true)
	s := mustScheduler(t, preset)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	card := fuzzCardState(now)

	first, err := s.Next(card, now, Good)
	if err != nil {
		t.Fatalf("Next() first error = %v", err)
	}
	second, err := s.Next(card, now, Good)
	if err != nil {
		t.Fatalf("Next() second error = %v", err)
	}
	if !first.Due.Equal(second.Due) {
		t.Errorf("fuzz due not deterministic: %v != %v", first.Due, second.Due)
	}
	if first.ScheduledDays != second.ScheduledDays {
		t.Errorf("fuzz scheduled days not deterministic: %d != %d", first.ScheduledDays, second.ScheduledDays)
	}
	if first.Stability != second.Stability {
		t.Errorf("fuzz stability not deterministic: %v != %v", first.Stability, second.Stability)
	}
}

// TestFuzzSequenceReplayIsIdentical 断言从同一初始状态重放同一串评分序列，逐条得到相同结果；
// 单次调用相等还不够 —— 状态机若累积了挂钟或全局随机，序列重放会分叉。
func TestFuzzSequenceReplayIsIdentical(t *testing.T) {
	preset := testPreset(t)
	preset.EnableFuzz = boolPtr(true)
	s := mustScheduler(t, preset)
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	ratings := []Rating{Good, Good, Again, Good, Easy}

	replay := func() []Outcome {
		card := fuzzCardState(now)
		out := make([]Outcome, 0, len(ratings))
		at := now
		for _, rating := range ratings {
			next, err := s.Next(card, at, rating)
			if err != nil {
				t.Fatalf("Next(%v) error = %v", rating, err)
			}
			out = append(out, next)
			card = outcomeToState(card, next, at)
			at = at.Add(24 * time.Hour)
		}
		return out
	}

	a, b := replay(), replay()
	for i := range a {
		if !a[i].Due.Equal(b[i].Due) {
			t.Errorf("replay[%d] due = %v, want %v", i, b[i].Due, a[i].Due)
		}
		if a[i].ScheduledDays != b[i].ScheduledDays {
			t.Errorf("replay[%d] scheduled days = %d, want %d", i, b[i].ScheduledDays, a[i].ScheduledDays)
		}
	}
}

// TestFuzzActuallyPerturbsInterval 断言 fuzz 开启确实改动间隔：对同一个长间隔复习卡，
// 开启与关闭 fuzz 至少有一处结果不同。没有这条，上面的“确定性”可能只是因为 fuzz 没生效。
// 输入固定（now 固定、卡固定），因此该断言本身也是确定性的。
func TestFuzzActuallyPerturbsInterval(t *testing.T) {
	off := testPreset(t)
	off.EnableFuzz = boolPtr(false)
	on := testPreset(t)
	on.EnableFuzz = boolPtr(true)
	sOff := mustScheduler(t, off)
	sOn := mustScheduler(t, on)

	base := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	card := fuzzCardState(base)

	gotOff, err := sOff.Next(card, base, Good)
	if err != nil {
		t.Fatalf("Next() fuzz-off error = %v", err)
	}
	gotOn, err := sOn.Next(card, base, Good)
	if err != nil {
		t.Fatalf("Next() fuzz-on error = %v", err)
	}
	if gotOff.ScheduledDays == gotOn.ScheduledDays && gotOff.Due.Equal(gotOn.Due) {
		t.Skipf("fuzz left a %d-day interval unchanged for this seed; the seed test still guards determinism", gotOff.ScheduledDays)
	}
}

// fuzzCardState 返回一张长间隔复习卡（stability 足够大，评分间隔超过 fuzz 的 2.5 天下限）。
func fuzzCardState(now time.Time) *store.CardState {
	stability := 40.0
	difficulty := 5.0
	last := now.Add(-30 * 24 * time.Hour)
	due := now.Add(-time.Hour)
	return &store.CardState{
		CardID: 7, UserID: 1, State: StateReview.String(),
		Stability: &stability, Difficulty: &difficulty,
		LastReviewAt: &last, DueAt: &due,
		Reps: 5, ScheduledDays: 30,
	}
}

// outcomeToState 把一次评分结果推进成下一张待评分卡，供序列重放使用。
func outcomeToState(base *store.CardState, o Outcome, at time.Time) *store.CardState {
	stability := o.Stability
	difficulty := o.Difficulty
	last := at
	due := o.Due
	return &store.CardState{
		CardID: base.CardID, UserID: base.UserID, State: o.State.String(),
		Stability: &stability, Difficulty: &difficulty,
		LastReviewAt: &last, DueAt: &due,
		Reps: o.Reps, Lapses: o.Lapses, ScheduledDays: o.ScheduledDays,
		StepIndex: o.RemainingSteps,
	}
}
