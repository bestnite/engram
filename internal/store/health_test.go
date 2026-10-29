package store

import (
	"context"
	"testing"
	"time"
)

// TestDueQueueSizeMatchesQueueRules pins the health page's due queue length to the rows the review queue would serve as due:
// a scheduled, unsuspended learning/review/relearning row whose due_at has passed. A buried new card (state=new with a past
// due_at) is a new card to the queue, so it must not count either.
func TestDueQueueSizeMatchesQueueRules(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		dueIn     time.Duration
		suspended bool
		want      int64
	}{
		{name: "due review card", state: "review", dueIn: -time.Hour, want: 1},
		{name: "due learning card", state: "learning", dueIn: -time.Minute, want: 1},
		{name: "review card not due yet", state: "review", dueIn: time.Hour, want: 0},
		{name: "suspended due card", state: "learning", dueIn: -time.Hour, suspended: true, want: 0},
		{name: "buried new card past its due_at", state: "new", dueIn: -time.Hour, want: 0},
	}
	for _, tc := range cases {
		for driver, db := range testDatabases(t) {
			t.Run(driver+"/"+tc.name, func(t *testing.T) {
				if err := db.AutoMigrate(AllModels()...); err != nil {
					t.Fatalf("AutoMigrate() error = %v", err)
				}
				now := time.Now().UTC()
				userID := seedReminderUser(t, db, "health", "health@example.com", now.Add(tc.dueIn), tc.suspended)
				if err := db.Model(&CardState{}).Where("user_id = ?", userID).Update("state", tc.state).Error; err != nil {
					t.Fatalf("set state: %v", err)
				}
				got, err := DueQueueSize(context.Background(), db, now)
				if err != nil {
					t.Fatalf("DueQueueSize() error = %v", err)
				}
				if got != tc.want {
					t.Errorf("DueQueueSize() = %d, want %d", got, tc.want)
				}
			})
		}
	}
}
