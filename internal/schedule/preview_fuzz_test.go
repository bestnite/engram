package schedule

import (
	"testing"
	"time"

	"git.nite07.com/nite/engram/internal/store"
)

// TestPreviewIgnoresFuzz asserts that for a preset with fuzz on, the preview gives the central value before fuzz:
// it matches Next's results rating by rating with fuzz turned off on the same preset, and does not vary with the millisecond-level preview instant.
// The intervals shown under the buttons on the review page come from the preview; with fuzz they would differ on every flip.
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
