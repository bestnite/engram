package store

import (
	"context"
	"testing"
	"time"
)

// TestLastReminderSentAtPicksMostRecent 验证最小间隔门的时间戳来源：跨复习日取最新的 SentAt，
// 从未发过时 ok=false。两个驱动都跑：这是发送方判「距上次发送多久」的唯一依据。
func TestLastReminderSentAtPicksMostRecent(t *testing.T) {
	for driver, db := range testDatabases(t) {
		t.Run(driver, func(t *testing.T) {
			if err := db.AutoMigrate(AllModels()...); err != nil {
				t.Fatalf("AutoMigrate() error = %v", err)
			}
			ctx := context.Background()

			// 从未发过：ok=false，不是零值时间。
			if _, ok, err := LastReminderSentAt(ctx, db, 42); err != nil {
				t.Fatalf("LastReminderSentAt(before): %v", err)
			} else if ok {
				t.Fatal("LastReminderSentAt(before) ok = true, want false")
			}

			older := time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)
			newer := older.Add(20 * time.Hour)
			// 先写较新的一天再写较旧的一天，确认是按 sent_at 取最大，而不是按插入顺序。
			if err := RecordReminderSent(ctx, db, 42, "2026-06-02", newer); err != nil {
				t.Fatalf("RecordReminderSent(newer): %v", err)
			}
			if err := RecordReminderSent(ctx, db, 42, "2026-06-01", older); err != nil {
				t.Fatalf("RecordReminderSent(older): %v", err)
			}
			// 另一个用户的行不该被算进这个用户。
			if err := RecordReminderSent(ctx, db, 43, "2026-06-02", newer.Add(time.Hour)); err != nil {
				t.Fatalf("RecordReminderSent(other user): %v", err)
			}

			got, ok, err := LastReminderSentAt(ctx, db, 42)
			if err != nil {
				t.Fatalf("LastReminderSentAt: %v", err)
			}
			if !ok {
				t.Fatal("LastReminderSentAt ok = false, want true")
			}
			if !got.Equal(newer) {
				t.Fatalf("LastReminderSentAt = %v, want %v (the most recent sent_at)", got, newer)
			}
		})
	}
}
