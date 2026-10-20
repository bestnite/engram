package schedule

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestPreviewIgnoresFuzz 断言开了 fuzz 的预设，预览给出的是 fuzz 之前的中心值：
// 与同一预设关掉 fuzz 后 Next 的结果逐档一致，并且不随毫秒级的预览时刻变化。
// 复习页按钮下显示的间隔来自预览，加了 fuzz 就会每次翻面都不一样。
func TestPreviewIgnoresFuzz(t *testing.T) {
	fuzzed := store.NewPreset(1, "fuzzed")
	fuzzed.EnableFuzz = boolPtr(true)
	plain := mustScheduler(t, testPreset(t))
	withFuzz := mustScheduler(t, &fuzzed)

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	last := now.AddDate(0, 0, -30)
	due := now
	stability, difficulty := 30.0, 5.0
	cases := []struct {
		name  string
		state *store.CardState
	}{
		{"new card", newTestCardState()},
		{"review card", &store.CardState{CardID: 1, UserID: 1, State: "review", DueAt: &due,
			Stability: &stability, Difficulty: &difficulty, LastReviewAt: &last, Reps: 5, ScheduledDays: 30}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, err := withFuzz.Preview(tc.state, now)
			if err != nil {
				t.Fatalf("Preview() error = %v", err)
			}
			later, err := withFuzz.Preview(tc.state, now.Add(time.Millisecond))
			if err != nil {
				t.Fatalf("Preview(+1ms) error = %v", err)
			}
			for i, rating := range []Rating{Again, Hard, Good, Easy} {
				want, err := plain.Next(tc.state, now, rating)
				if err != nil {
					t.Fatalf("Next(%v) error = %v", rating, err)
				}
				if !first[i].Due.Equal(want.Due) {
					t.Errorf("Preview()[%v].Due = %v, want the unfuzzed %v", rating, first[i].Due, want.Due)
				}
				if got := later[i].Due.Sub(first[i].Due); got != time.Millisecond {
					t.Errorf("Preview(+1ms)[%v] moved by %v, want exactly 1ms (no fuzz)", rating, got)
				}
			}
		})
	}
}
